package site

import (
	"encoding/json"
	"log"
	"net/http"
	"regexp"
	"strings"

	"xgift/internal/checkout"
)

var cardLast4Pattern = regexp.MustCompile(`^[0-9]{4}$`)

// cardAddRequest is the exact shape the dashboard sends. Billing fields are
// optional: on an empty vault the operator must supply them (there's no
// existing card to inherit from); on a populated vault inheritBilling fills
// anything blank with the first configured card's address profile.
type cardAddRequest struct {
	Number    string  `json:"number"`
	ExpMonth  string  `json:"exp_month"`
	ExpYear   string  `json:"exp_year"`
	CVC       string  `json:"cvc"`
	Limit     float64 `json:"limit"`
	Name      string  `json:"billing_name,omitempty"`
	Email     string  `json:"email,omitempty"`
	Country   string  `json:"billing_country,omitempty"`
	Postal    string  `json:"billing_postal_code,omitempty"`
	Line1     string  `json:"billing_address_line1,omitempty"`
	Line2     string  `json:"billing_address_line2,omitempty"`
	City      string  `json:"billing_city,omitempty"`
	State     string  `json:"billing_state,omitempty"`
}

// cardFromRequest canonicalises the admin request into the JSON shape
// AddCardRecords expects (same field names as the stored card record).
func (q cardAddRequest) toJSON() ([]byte, error) {
	payload := map[string]any{
		"number":    strings.ReplaceAll(q.Number, " ", ""),
		"exp_month": q.ExpMonth,
		"exp_year":  q.ExpYear,
		"cvc":       q.CVC,
		"limit":     q.Limit,
	}
	// 只在前端显式传了 billing 字段时放进 payload,否则后端 inheritBilling
	// 会从已有卡填充;空字符串和 "未提交" 行为必须一致。
	for k, v := range map[string]string{
		"billing_name":          q.Name,
		"email":                 q.Email,
		"billing_country":       q.Country,
		"billing_postal_code":   q.Postal,
		"billing_address_line1": q.Line1,
		"billing_address_line2": q.Line2,
		"billing_city":          q.City,
		"billing_state":         q.State,
	} {
		if strings.TrimSpace(v) != "" {
			payload[k] = v
		}
	}
	return json.Marshal(payload)
}

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

// cardsAdd: POST /api/admin/cards — 添加 **一张** 卡;Name/Email/Country
// 等 billing 字段可以留空由后端 inheritBilling 从已有卡继承,也可以显式
// 传(空卡池首单必须显式传)。严格 decode:拒绝 JSON 数组、尾随字符、
// 非 application/json。
func (s *server) cardsAdd(w http.ResponseWriter, r *http.Request) {
	var q cardAddRequest
	if !decode(w, r, &q) {
		return
	}
	raw, err := q.toJSON()
	if err != nil {
		message(w, 500, "请求序列化失败。")
		return
	}
	n, err := checkout.AddCardRecords(s.vault, raw)
	if err != nil {
		message(w, 400, err.Error())
		return
	}
	log.Printf("admin cards add: last4=%s total=%d", lastN(q.Number, 4), n)
	reply(w, 200, map[string]any{"count": n, "message": "卡已添加。"})
}

type last4Request struct {
	Last4 string `json:"last4"`
}

// cardsRemove: POST /api/admin/cards/remove {"last4":"1234"} —— 从卡池移除。
func (s *server) cardsRemove(w http.ResponseWriter, r *http.Request) {
	var q last4Request
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
	log.Printf("admin cards remove: last4=%s remaining=%d", q.Last4, n)
	reply(w, 200, map[string]any{"remaining": n, "message": "卡已移除。"})
}

// cardsUnblock: POST /api/admin/cards/unblock —— 解除卡池的 block 和冷却。
// 可选 {last4} 只解除某一张;留空解除全部(保留历史兼容)。
func (s *server) cardsUnblock(w http.ResponseWriter, r *http.Request) {
	var q last4Request
	if !decode(w, r, &q) {
		return
	}
	if q.Last4 != "" && !cardLast4Pattern.MatchString(q.Last4) {
		message(w, 400, "尾号必须是 4 位数字。")
		return
	}
	n, err := checkout.UnblockPaymentCards(s.vault, q.Last4)
	if err != nil {
		message(w, 503, "解除失败。")
		return
	}
	msg := "已解除卡池冷却/封禁。"
	if q.Last4 != "" {
		msg = "已解除该卡的冷却/封禁。"
		log.Printf("admin cards unblock: last4=%s cleared=%d", q.Last4, n)
	} else {
		log.Printf("admin cards unblock-all: cleared=%d", n)
	}
	reply(w, 200, map[string]any{"cleared": n, "message": msg})
}

// lastN safely tails a string by n characters. Non-digit inputs are
// trimmed to the final n runes.
func lastN(s string, n int) string {
	s = strings.ReplaceAll(s, " ", "")
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
