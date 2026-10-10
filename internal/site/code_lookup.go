package site

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// codeLookup returns the persisted redemption state to the holder of the full
// code. It never rechecks a provider, enqueues work, or starts a payment. Keep
// account names masked and omit internal IDs, raw messages, and payment data.
func (s *server) codeLookup(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Code string `json:"code"`
	}
	if !decode(w, r, &q) {
		return
	}
	q.Code = strings.ToUpper(strings.TrimSpace(q.Code))
	if !codePattern.MatchString(q.Code) {
		reply(w, http.StatusBadRequest, map[string]any{"error": "invalid_code", "message": "请填写完整兑换码。"})
		return
	}
	var status, username, queueState string
	var months, progress, ahead int
	var updated int64
	// Read the code and its current queue in one snapshot. A queued code is
	// still active in codes until the worker claims it.
	err := s.db.QueryRowContext(r.Context(), `SELECT c.status,c.months,c.progress,c.updated,
 COALESCE(q.username,c.username),COALESCE(q.state,''),
 (SELECT COUNT(*) FROM redeem_queue ahead WHERE ahead.state IN ('queued','running') AND ahead.seq < q.seq)
 FROM codes c LEFT JOIN redeem_queue q ON q.code_id=c.id AND q.state IN ('queued','running')
 WHERE c.hash=?`, hash(q.Code)).Scan(&status, &months, &progress, &updated, &username, &queueState, &ahead)
	if errors.Is(err, sql.ErrNoRows) {
		reply(w, http.StatusNotFound, map[string]any{"error": "not_found", "message": "未找到此卡密，请核对后重试。"})
		return
	}
	if err != nil {
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "service_unavailable", "message": "暂时无法查询卡密，请稍后重试。"})
		return
	}
	queued := queueState == "queued" && status != "succeeded" && status != "revoked"
	if queued || queueState == "running" && status != "succeeded" && status != "revoked" {
		status = "processing"
		progress = max(5, progress)
	}
	var msg string
	switch status {
	case "active":
		msg = "卡密尚未兑换，可返回兑换页继续。"
	case "processing":
		msg = "正在办理中，请稍后查询，无需重复提交。"
	case "succeeded":
		progress = 100
		msg = "此卡密已完成兑换，请登录接收账号查看 Premium。蓝色认证标识以 X 审核为准。"
	case "review":
		msg = "兑换结果待核实，请保留卡密并联系购买商家，无需重复提交。"
	case "revoked":
		msg = "此卡密已停用，请联系购买商家。"
	default:
		reply(w, http.StatusServiceUnavailable, map[string]any{"error": "service_unavailable", "message": "暂时无法确认卡密状态，请稍后重试。"})
		return
	}
	result := map[string]any{
		"status": status, "months": months, "label": fmt.Sprintf("%d 个月 Premium", months),
		"progress": progress, "updated": updated, "username_masked": maskLookupUsername(username),
		"queued": queued, "message": msg,
	}
	if queued {
		result["ahead"] = ahead
		result["message"] = fmt.Sprintf("已排队，前面还有 %d 单，请稍后查询。", ahead)
	}
	reply(w, http.StatusOK, result)
}

func maskLookupUsername(username string) string {
	if username == "" {
		return ""
	}
	if len(username) < 4 {
		return "@***"
	}
	return "@" + username[:2] + "***" + username[len(username)-1:]
}
