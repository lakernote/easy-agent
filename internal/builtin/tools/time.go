package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
)

func currentTimeTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name: "current_time", Description: "读取精确当前时间和时区；需要精确到时分秒时使用。",
			Parameters: objectSchema(map[string]any{"timezone": stringSchema("可选 IANA 时区，例如 Asia/Shanghai；留空使用服务器时区")}, nil),
		},
		Run: func(_ context.Context, raw json.RawMessage) (string, error) {
			var arguments struct {
				Timezone string `json:"timezone"`
			}
			if len(raw) > 0 {
				if err := json.Unmarshal(raw, &arguments); err != nil {
					return "", err
				}
			}
			location := time.Local
			if name := strings.TrimSpace(arguments.Timezone); name != "" {
				loaded, err := time.LoadLocation(name)
				if err != nil {
					return "", fmt.Errorf("无效时区 %q，请使用 Asia/Shanghai 这样的 IANA 时区", name)
				}
				location = loaded
			}
			now := time.Now().In(location)
			zone, offset := now.Zone()
			value := map[string]any{
				"datetime": now.Format(time.RFC3339), "date": now.Format("2006-01-02"), "time": now.Format("15:04:05"),
				"weekday": weekday(now.Weekday()), "timezone": now.Location().String(), "zone": zone,
				"utc_offset": fmt.Sprintf("%+03d:%02d", offset/3600, abs(offset%3600)/60),
			}
			data, _ := json.MarshalIndent(value, "", "  ")
			return string(data), nil
		},
	}
}

func weekday(day time.Weekday) string {
	return [...]string{"星期日", "星期一", "星期二", "星期三", "星期四", "星期五", "星期六"}[day]
}

func abs(value int) int {
	if value < 0 {
		return -value
	}
	return value
}
