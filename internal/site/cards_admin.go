package site

import (
	"encoding/json"
	"net/http"
	"regexp"

	"xgift/internal/checkout"
)

var cardLast4Pattern = regexp.MustCompile(`^[0-9]{4}$`)

// cardsList: GET /api/admin/cards — number-free卡池视图,带可用额度/消费聚合。
func (s *server) cardsList(w http.ResponseWriter, r *http.Request) {
	status, err := checkout.CardsStatus(s.vault)
	if err != nil {
		message(w, 503, "读取卡池失败。")
		return
	}
	if status == nil {
		status = []checkout.CardStatus{}
	}
	reply(w, 200, map[string]any{"cards": status})
}

// cardsAdd: POST /api/admin/cards — 添加一张卡;Name/Email/Country 等 billing
// 字段由后端从已有卡继承(AddCardRecords(inherit=true))。
// 入参 JSON:{number, exp_month, exp_year, cvc, limit} —— 5 字段,limit 必填 > 0。
func (s *server) cardsAdd(w http.ResponseWriter, r *http.Request) {
	raw, err := readAdminBody(r)
	if err != nil {
		message(w, 400, err.Error())
		return
	}
	n, err := checkout.AddCardRecords(s.vault, raw)
	if err != nil {
		message(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"count": n, "message": "卡已添加。"})
}

// cardsRemove: POST /api/admin/cards/remove {"last4":"1234"} —— 从卡池移除。
func (s *server) cardsRemove(w http.ResponseWriter, r *http.Request) {
	var q struct {
		Last4 string `json:"last4"`
	}
	if !decode(w, r, &q) {
		return
	}
	if !cardLast4Pattern.MatchString(q.Last4) {
		message(w, 400, "尾号必须是 4 位数字。")
		return
	}
	n, err := checkout.RemoveCardRecord(s.vault, q.Last4)
	if err != nil {
		message(w, 400, err.Error())
		return
	}
	reply(w, 200, map[string]any{"remaining": n, "message": "卡已移除。"})
}

// cardsUnblock: POST /api/admin/cards/unblock —— 清空所有卡的 block/cooldown。
// 当前后端 UnblockPaymentCards 一次清掉所有记录,不按 last4 粒度;若后面需要
// 更细可以再加 last4 分支。入参留空兼容未来扩展。
func (s *server) cardsUnblock(w http.ResponseWriter, r *http.Request) {
	n, err := checkout.UnblockPaymentCards(s.vault)
	if err != nil {
		message(w, 503, "解除失败。")
		return
	}
	reply(w, 200, map[string]any{"cleared": n, "message": "已解除卡池冷却/封禁。"})
}

// readAdminBody 读小 JSON body,上限 16 KiB 够一张卡 + 继承字段。
func readAdminBody(r *http.Request) ([]byte, error) {
	const maxBytes = 16 << 10
	r.Body = http.MaxBytesReader(nil, r.Body, maxBytes)
	defer r.Body.Close()
	var raw json.RawMessage
	if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
		return nil, err
	}
	return []byte(raw), nil
}
