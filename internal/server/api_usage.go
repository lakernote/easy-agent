package server

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

type usageReport struct {
	Period      string                 `json:"period"`
	From        time.Time              `json:"from"`
	To          time.Time              `json:"to"`
	GeneratedAt time.Time              `json:"generatedAt"`
	Items       []store.UsageAggregate `json:"items"`
}

// usage 返回已经落库的运行事件统计。统计接口独立于 bootstrap，避免每次打开
// 对话都把历史用量塞进首屏；前端只在设置中心的“用量”页按需请求。
func (server *Server) usage(response http.ResponseWriter, request *http.Request) {
	period := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("period")))
	if period == "" {
		period = "day"
	}
	if period != "day" && period != "week" && period != "month" {
		writeError(response, http.StatusBadRequest, "统计周期必须是 day、week 或 month")
		return
	}

	days := usageDefaultDays(period)
	if raw := strings.TrimSpace(request.URL.Query().Get("days")); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 || value > 366 {
			writeError(response, http.StatusBadRequest, "统计范围必须是 1 到 366 天")
			return
		}
		days = value
	}
	now := time.Now()
	from := now.AddDate(0, 0, -days)
	items, err := server.store.UsageAggregates(period, from, now.Add(time.Second))
	if err != nil {
		writeError(response, http.StatusInternalServerError, "读取用量统计失败: "+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, usageReport{Period: period, From: from, To: now, GeneratedAt: time.Now(), Items: items})
}

func usageDefaultDays(period string) int {
	switch period {
	case "week":
		return 84
	case "month":
		return 366
	default:
		return 30
	}
}
