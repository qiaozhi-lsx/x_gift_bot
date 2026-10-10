package site

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"xgift/internal/checkout"
)

// All redemptions (single and batch, every visitor) pass through one FIFO
// queue so a payment is only ever in flight on the single order slot. The
// queue table survives restarts; at most maxRedeemBatch entries are accepted
// per submission and the queue itself is capped at maxQueueDepth.
const (
	maxRedeemBatch  = 5
	maxQueueDepth   = 200
	queueRetention  = 24 * time.Hour
	queueClaimWait  = 15 * time.Minute
	queuePollPeriod = 2 * time.Second
)

type queueRow struct {
	Seq       int64
	ID        string
	Token     string
	CodeID    string
	Username  string
	Recipient string
	Resuming  bool
}

func migrateRedeemQueue(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS redeem_queue (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 token TEXT NOT NULL,
 code_id TEXT NOT NULL,
 username TEXT NOT NULL,
 recipient TEXT NOT NULL DEFAULT '',
 resuming INTEGER NOT NULL DEFAULT 0,
 state TEXT NOT NULL CHECK(state IN ('queued','running','done','failed')),
 message TEXT NOT NULL DEFAULT '',
 created INTEGER NOT NULL
 );
 CREATE INDEX IF NOT EXISTS redeem_queue_state ON redeem_queue(state, seq);
 CREATE INDEX IF NOT EXISTS redeem_queue_token ON redeem_queue(token);
 CREATE UNIQUE INDEX IF NOT EXISTS redeem_queue_one_active ON redeem_queue(code_id) WHERE state IN ('queued','running');`)
	return err
}

// restoreRedeemQueue reconciles leftovers from a crash: rows whose code never
// left the queue are put back, rows whose code moved on are finished (the code
// state itself is reconciled elsewhere).
func (s *server) restoreRedeemQueue() error {
	if _, err := s.db.Exec("UPDATE redeem_queue SET state='done', message='服务重启，订单已转人工核实。' WHERE state='running' AND code_id IN (SELECT id FROM codes WHERE status!='active')"); err != nil {
		return err
	}
	_, err := s.db.Exec("UPDATE redeem_queue SET state='queued' WHERE state='running'")
	return err
}

func (s *server) sweepRedeemQueue() {
	cutoff := time.Now().Add(-queueRetention).Unix()
	if _, err := s.db.Exec("DELETE FROM redeem_queue WHERE state IN ('done','failed') AND created < ?", cutoff); err != nil {
		log.Printf("redeem queue cleanup failed")
	}
}

// redeemQueueLoop is the single worker that drains the queue in order.
func (s *server) redeemQueueLoop() {
	ticker := time.NewTicker(queuePollPeriod)
	defer ticker.Stop()
	sweep := time.Now()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-ticker.C:
		}
		if time.Since(sweep) > 10*time.Minute {
			sweep = time.Now()
			s.sweepRedeemQueue()
		}
		ready, err := s.paymentsAvailable()
		if err != nil || !ready {
			continue
		}
		var q queueRow
		var resuming int
		e := s.db.QueryRow("SELECT seq,id,token,code_id,username,recipient,resuming FROM redeem_queue WHERE state='queued' ORDER BY seq LIMIT 1").
			Scan(&q.Seq, &q.ID, &q.Token, &q.CodeID, &q.Username, &q.Recipient, &resuming)
		if errors.Is(e, sql.ErrNoRows) {
			continue
		}
		if e != nil {
			log.Printf("redeem queue could not be read")
			continue
		}
		q.Resuming = resuming == 1
		res, e := s.db.Exec("UPDATE redeem_queue SET state='running' WHERE seq=? AND state='queued'", q.Seq)
		if e != nil {
			continue
		}
		if n, e := res.RowsAffected(); e != nil || n != 1 {
			continue
		}
		s.processQueueRow(q)
	}
}

func (s *server) processQueueRow(q queueRow) {
	fail := func(msg string) {
		if _, err := s.db.Exec("UPDATE redeem_queue SET state='failed', message=? WHERE seq=?", msg, q.Seq); err != nil {
			log.Printf("queue row %d could not record failure", q.Seq)
		}
	}
	c, e := s.findByID(q.CodeID)
	if errors.Is(e, sql.ErrNoRows) {
		fail("兑换码不存在。")
		return
	}
	if e != nil {
		fail("无法读取兑换码，请稍后重试。")
		return
	}
	resuming := q.Resuming
	if !resuming && c.Status != "active" {
		if _, err := s.db.Exec("UPDATE redeem_queue SET state='done', message=? WHERE seq=?", "兑换码已在使用或已完成。", q.Seq); err != nil {
			log.Printf("queue row %d could not be finished", q.Seq)
		}
		return
	}
	recipient := q.Recipient
	if !resuming {
		var elErr error
		for attempt := 0; attempt < 2; attempt++ {
			if s.ctx.Err() != nil {
				return
			}
			ctx, cancel := context.WithTimeout(s.ctx, 35*time.Second)
			recipient, elErr = checkout.Eligibility(ctx, s.vault, q.Username, s.port)
			cancel()
			if elErr == nil || errors.Is(elErr, checkout.ErrNotEligible) || errors.Is(elErr, checkout.ErrUserNotFound) {
				break
			}
			time.Sleep(4 * time.Second)
		}
		if elErr != nil {
			switch {
			case errors.Is(elErr, checkout.ErrNotEligible):
				fail("X 当前不允许向这个账号赠送 Premium，兑换码未使用，可更换符合条件的账号。")
			case errors.Is(elErr, checkout.ErrUserNotFound):
				fail("未能找到这个 X 账号，兑换码未使用，请核对用户名。")
			default:
				fail("暂时无法向 X 核实赠送资格，兑换码未使用，请重新提交。")
			}
			return
		}
	}
	release, ok := s.waitLock(s.ctx, queueClaimWait)
	if !ok {
		// Shutting down or the slot stayed busy far too long: put the row back.
		if _, err := s.db.Exec("UPDATE redeem_queue SET state='queued' WHERE seq=?", q.Seq); err != nil {
			log.Printf("queue row %d could not be requeued", q.Seq)
		}
		return
	}
	defer release()
	if !resuming {
		// A prior CLI order must not fulfill a newly presented redemption code.
		for _, key := range []string{"checkout:" + recipient, "checkout:" + q.Username} {
			if _, e = s.vault.Get(key); !errors.Is(e, sql.ErrNoRows) {
				fail("这个账号已有订单记录，需要管理员核实后处理，兑换码未使用。")
				return
			}
		}
	}
	var result sql.Result
	if resuming {
		result, e = s.db.Exec("UPDATE codes SET status='processing',progress=20,message=?,updated=? WHERE id=? AND status='review' AND username=? AND recipient_id=? AND months=?", "正在重新检查原订单，请稍候。", time.Now().Unix(), c.ID, q.Username, recipient, c.Months)
	} else {
		result, e = s.db.Exec("UPDATE codes SET status='processing',progress=20,username=?,recipient_id=?,message=?,updated=? WHERE id=? AND status='active'", q.Username, recipient, "正在处理，请不要重复提交。", time.Now().Unix(), c.ID)
	}
	if e != nil {
		fail("无法开始处理订单，请刷新后查询兑换状态。")
		return
	}
	if n, e := result.RowsAffected(); e != nil || n != 1 {
		if _, err := s.db.Exec("UPDATE redeem_queue SET state='done', message=? WHERE seq=?", "兑换码状态已改变。", q.Seq); err != nil {
			log.Printf("queue row %d could not be finished", q.Seq)
		}
		return
	}
	c.Username, c.RecipientID = q.Username, recipient
	s.executeRedemption(c, q.Username, recipient, resuming)
	var st, msg string
	if err := s.db.QueryRow("SELECT status,message FROM codes WHERE id=?", c.ID).Scan(&st, &msg); err != nil {
		msg = "订单已处理，请查询兑换进度。"
	}
	if _, err := s.db.Exec("UPDATE redeem_queue SET state='done', message=? WHERE seq=?", msg, q.Seq); err != nil {
		log.Printf("queue row %d could not be finished", q.Seq)
	}
}

// executeRedemption runs the checkout flow for one claimed code. The caller
// holds the payment lock for the whole call.
func (s *server) executeRedemption(c codeRow, user, recipient string, resuming bool) {
	ctx, cancel := context.WithTimeout(s.ctx, 240*time.Second)
	defer cancel()
	ctx = checkout.WithProgress(ctx, func(percent int, msg string) {
		if _, err := s.db.Exec("UPDATE codes SET progress=MAX(progress,?),message=?,updated=? WHERE id=? AND status='processing'", percent, msg, time.Now().Unix(), c.ID); err != nil {
			log.Printf("order %s progress could not be saved", c.ID)
		}
	})
	var record *checkout.Record
	var err error
	if resuming {
		record, err = checkout.ResumeForRecipient(ctx, s.vault, user, recipient, s.port, c.Months)
	} else {
		record, err = checkout.RunForRecipient(ctx, s.vault, user, recipient, true, s.port, c.Months)
	}
	if err != nil {
		stage := "before_order"
		if record != nil {
			stage = record.Status
		}
		failure, marshalErr := json.Marshal(map[string]any{"code_id": c.ID, "recipient_id": recipient, "months": c.Months, "stage": stage, "error": err.Error(), "observed_at": time.Now().Unix()})
		if marshalErr != nil || s.vault.Put(fmt.Sprintf("redemption-failure:%s:%d", c.ID, time.Now().UnixNano()), failure) != nil {
			log.Printf("order %s failure details could not be persisted", c.ID)
		}
		clear(failure)
		log.Printf("order %s stopped at stage %s; upstream details remain encrypted", c.ID, stage)
	}
	status, msg := "review", redeemMessage(record, err)
	if err == nil && record != nil && record.Status == "succeeded" && record.RecipientID == recipient && record.Months == c.Months {
		plan, planErr := s.catalogPlan(c.Months)
		if planErr == nil && record.Amount == plan.Minor && record.Currency == strings.ToUpper(plan.Currency) {
			status, msg = "succeeded", fmt.Sprintf("已为 @%s 完成 %d 个月 Premium 赠送。", user, c.Months)
		}
	}
	updated, e := s.db.Exec("UPDATE codes SET status=?,message=?,updated=?,progress=CASE WHEN ?='succeeded' THEN 100 ELSE progress END WHERE id=? AND status='processing'", status, msg, time.Now().Unix(), status, c.ID)
	if e != nil {
		log.Printf("order %s requires database reconciliation", c.ID)
		return
	}
	n, e := updated.RowsAffected()
	if e != nil || n != 1 {
		log.Printf("order %s requires database reconciliation", c.ID)
		return
	}
	// Never log the checkout URL, credentials or upstream payloads.
	log.Printf("order %s finished: %s", c.ID, status)
}

func (s *server) findByID(id string) (codeRow, error) {
	var c codeRow
	e := s.db.QueryRow("SELECT id,hint,batch,months,status,username,message,created,updated,progress,COALESCE(recipient_id,'') FROM codes WHERE id=?", id).
		Scan(&c.ID, &c.Hint, &c.Batch, &c.Months, &c.Status, &c.Username, &c.Message, &c.Created, &c.Updated, &c.Progress, &c.RecipientID)
	return c, e
}

type redeemRequestItem struct {
	Code     string `json:"code"`
	Username string `json:"username"`
}

// enqueueRedemptions validates every submitted pair and appends queue rows.
// Single redemption and batch redemption share this path.
func (s *server) enqueueRedemptions(w http.ResponseWriter, r *http.Request, items []redeemRequestItem) {
	ready, availabilityErr := s.paymentsAvailable()
	if !ready || availabilityErr != nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"status": "paused", "message": "充值暂时暂停，恢复时间待定。请保留兑换码，已有订单可继续查询进度。"})
		return
	}
	if len(items) == 0 || len(items) > maxRedeemBatch {
		message(w, 400, fmt.Sprintf("每次提交 1–%d 组兑换码和用户名。", maxRedeemBatch))
		return
	}
	type prepared struct {
		user     string
		resuming bool
		c        codeRow
	}
	list := make([]prepared, 0, len(items))
	seen := make(map[string]bool, len(items))
	for i, it := range items {
		code := strings.ToUpper(strings.TrimSpace(it.Code))
		user, validUser := checkout.NormalizeUsername(it.Username)
		if !codePattern.MatchString(code) || !validUser {
			message(w, 400, fmt.Sprintf("第 %d 组：请填写完整的兑换码（xPP- 开头）和正确的 X 用户名（不是显示名称）。", i+1))
			return
		}
		if seen[code] {
			message(w, 400, fmt.Sprintf("第 %d 组：同一个兑换码重复提交了。", i+1))
			return
		}
		seen[code] = true
		c, e := s.find(code)
		if errors.Is(e, sql.ErrNoRows) {
			message(w, 404, fmt.Sprintf("第 %d 组：兑换码不存在，请检查后重试。", i+1))
			return
		}
		if e != nil {
			message(w, 503, fmt.Sprintf("第 %d 组：服务暂时不可用，兑换码未使用。", i+1))
			return
		}
		resuming := c.Status == "review" && c.Username == user && c.RecipientID != ""
		if c.Status != "active" && !resuming {
			if c.Username == user && (c.Status == "processing" || c.Status == "succeeded") {
				message(w, 409, fmt.Sprintf("第 %d 组：兑换码正在处理或已完成，请前往「查询开通结果」查看原订单。", i+1))
				return
			}
			message(w, 409, fmt.Sprintf("第 %d 组：兑换码已使用或已停用，请联系提供方。", i+1))
			return
		}
		list = append(list, prepared{user: user, resuming: resuming, c: c})
	}
	var depth int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM redeem_queue WHERE state IN ('queued','running')").Scan(&depth); err != nil {
		message(w, 503, "排队服务暂时不可用，请稍后重试。")
		return
	}
	if depth+len(list) > maxQueueDepth {
		message(w, 503, "当前排队人数较多，请稍后再试。")
		return
	}
	batchToken := token(16)
	now := time.Now().Unix()
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		message(w, 503, "排队服务暂时不可用，请稍后重试。")
		return
	}
	defer tx.Rollback()
	entries := make([]map[string]any, 0, len(list))
	for _, p := range list {
		resuming := 0
		if p.resuming {
			resuming = 1
		}
		if _, e := tx.Exec("INSERT INTO redeem_queue(id,token,code_id,username,recipient,resuming,state,created) VALUES(?,?,?,?,?,?,'queued',?)",
			token(8), batchToken, p.c.ID, p.user, p.c.RecipientID, resuming, now); e != nil {
			if strings.Contains(e.Error(), "redeem_queue_one_active") {
				message(w, 409, "有兑换码已在排队中，请稍后刷新查询。")
				return
			}
			message(w, 503, "排队失败，兑换码未使用，请稍后重试。")
			return
		}
		entries = append(entries, map[string]any{"hint": p.c.Hint, "months": p.c.Months, "username": p.user})
	}
	if err = tx.Commit(); err != nil {
		message(w, 503, "排队结果未保存，请稍后重试。")
		return
	}
	reply(w, 202, map[string]any{
		"status":   "processing",
		"progress": 10,
		"token":    batchToken,
		"items":    entries,
		"months":   list[0].c.Months,
		"queued":   len(list),
		"message":  "已加入处理队列，请保留本页等待结果。",
	})
}

// redeem handles one code (legacy single submission) through the same queue.
func (s *server) redeem(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Code     string `json:"code"`
		Username string `json:"username"`
	}
	if !decode(w, r, &q) {
		return
	}
	s.enqueueRedemptions(w, r, []redeemRequestItem{{Code: q.Code, Username: q.Username}})
}

// redeemBatch accepts up to maxRedeemBatch code/username pairs at once.
func (s *server) redeemBatch(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Items []redeemRequestItem `json:"items"`
	}
	if !decode(w, r, &q) {
		return
	}
	s.enqueueRedemptions(w, r, q.Items)
}

// batchStatus returns per-code progress for one queued submission.
func (s *server) batchStatus(w http.ResponseWriter, r *http.Request) {
	tok := r.PathValue("token")
	if len(tok) < 8 || len(tok) > 64 {
		message(w, 400, "查询参数无效。")
		return
	}
	rows, err := s.db.Query(`SELECT q.seq,q.username,q.state,q.message,c.hint,c.status,c.progress,c.message,c.months
 FROM redeem_queue q JOIN codes c ON c.id = q.code_id WHERE q.token=? ORDER BY q.seq`, tok)
	if err != nil {
		message(w, 503, "暂时无法查询队列，请稍后重试。")
		return
	}
	defer rows.Close()
	items := make([]map[string]any, 0, maxRedeemBatch)
	done := 0
	total := 0
	for rows.Next() {
		var seq int64
		var username, qstate, qmsg, hint, cstatus, cmsg string
		var progress, months int
		if err = rows.Scan(&seq, &username, &qstate, &qmsg, &hint, &cstatus, &progress, &cmsg, &months); err != nil {
			message(w, 503, "暂时无法查询队列，请稍后重试。")
			return
		}
		total++
		entry := map[string]any{"hint": hint, "username": username, "months": months}
		switch qstate {
		case "queued":
			var ahead int
			_ = s.db.QueryRow("SELECT COUNT(*) FROM redeem_queue WHERE state IN ('queued','running') AND seq < ?", seq).Scan(&ahead)
			entry["status"] = "queued"
			entry["progress"] = 5
			entry["ahead"] = ahead
			entry["message"] = fmt.Sprintf("已排队，前面还有 %d 单，请保留本页等待。", ahead)
		case "running":
			entry["status"] = "processing"
			entry["progress"] = max(progress, 20)
			entry["message"] = cmsg
			if entry["message"] == "" {
				entry["message"] = "正在处理，请勿关闭本页。"
			}
		case "failed":
			entry["status"] = "failed"
			entry["progress"] = progress
			entry["message"] = qmsg
		default: // done
			entry["status"] = cstatus
			entry["progress"] = progress
			if cstatus == "succeeded" {
				entry["progress"] = 100
			}
			entry["message"] = cmsg
		}
		if qstate == "done" || qstate == "failed" {
			done++
		}
		items = append(items, entry)
	}
	if err = rows.Err(); err != nil {
		message(w, 503, "暂时无法查询队列，请稍后重试。")
		return
	}
	if total == 0 {
		message(w, 404, "查询批次不存在或已过期。")
		return
	}
	reply(w, 200, map[string]any{"items": items, "done": done, "total": total, "payments_enabled": s.payments})
}

// codeInfoBatch is the read-only lookup for one or more codes, used by the
// redemption page before a batch is submitted.
func (s *server) codeInfoBatch(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Codes []string `json:"codes"`
	}
	if !decode(w, r, &q) {
		return
	}
	if len(q.Codes) == 0 || len(q.Codes) > maxRedeemBatch {
		message(w, 400, fmt.Sprintf("每次最多校验 %d 个兑换码。", maxRedeemBatch))
		return
	}
	results := make([]map[string]any, 0, len(q.Codes))
	for _, raw := range q.Codes {
		code := strings.ToUpper(strings.TrimSpace(raw))
		if !codePattern.MatchString(code) {
			results = append(results, map[string]any{"ok": false, "message": "请填写完整兑换码。"})
			continue
		}
		c, e := s.find(code)
		if errors.Is(e, sql.ErrNoRows) {
			results = append(results, map[string]any{"ok": false, "message": "兑换码不存在，请检查后重试。"})
			continue
		}
		if e != nil {
			results = append(results, map[string]any{"ok": false, "message": "服务暂时不可用，请稍后重试。"})
			continue
		}
		results = append(results, map[string]any{
			"ok":      true,
			"status":  c.Status,
			"months":  c.Months,
			"label":   fmt.Sprintf("%d 个月 Premium", c.Months),
			"message": "卡密有效。",
		})
	}
	reply(w, 200, map[string]any{"results": results})
}
