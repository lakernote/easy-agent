package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/lakernote/easy-agent/internal/agent"
)

const maxHTTPResult = 512 * 1024

func weatherTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name: "weather", Description: "查询指定城市的实时天气，数据来自 Open-Meteo。",
			Parameters: objectSchema(map[string]any{"location": stringSchema("城市或地区，例如 上海、北京、Shenzhen")}, []string{"location"}),
		},
		Run: func(ctx context.Context, raw json.RawMessage) (string, error) {
			var arguments struct {
				Location string `json:"location"`
			}
			if err := json.Unmarshal(raw, &arguments); err != nil {
				return "", err
			}
			return queryWeather(ctx, arguments.Location)
		},
	}
}

func queryWeather(ctx context.Context, location string) (string, error) {
	location = strings.TrimSpace(location)
	if location == "" {
		return "", errors.New("location 不能为空")
	}
	client := &http.Client{Timeout: 12 * time.Second}
	var geo struct {
		Results []struct {
			Name, Country, Admin1, Timezone string
			Latitude, Longitude             float64
		} `json:"results"`
	}
	if err := getJSON(ctx, client, "https://geocoding-api.open-meteo.com/v1/search?name="+url.QueryEscape(location)+"&count=1&language=zh&format=json", &geo); err != nil {
		return "", fmt.Errorf("查询地点失败: %w", err)
	}
	if len(geo.Results) == 0 {
		return "", fmt.Errorf("没有找到地点 %q", location)
	}
	place := geo.Results[0]
	endpoint := fmt.Sprintf("https://api.open-meteo.com/v1/forecast?latitude=%f&longitude=%f&current=temperature_2m,apparent_temperature,relative_humidity_2m,weather_code,wind_speed_10m&timezone=auto", place.Latitude, place.Longitude)
	var forecast struct {
		Timezone string `json:"timezone"`
		Current  struct {
			Time        string  `json:"time"`
			Temperature float64 `json:"temperature_2m"`
			Apparent    float64 `json:"apparent_temperature"`
			Humidity    float64 `json:"relative_humidity_2m"`
			WeatherCode int     `json:"weather_code"`
			Wind        float64 `json:"wind_speed_10m"`
		} `json:"current"`
	}
	if err := getJSON(ctx, client, endpoint, &forecast); err != nil {
		return "", fmt.Errorf("查询天气失败: %w", err)
	}
	value := map[string]any{
		"location":    map[string]any{"query": location, "name": place.Name, "admin1": place.Admin1, "country": place.Country},
		"observed_at": forecast.Current.Time, "timezone": forecast.Timezone, "condition": weatherText(forecast.Current.WeatherCode),
		"temperature_c": forecast.Current.Temperature, "feels_like_c": forecast.Current.Apparent,
		"humidity_percent": forecast.Current.Humidity, "wind_kmh": forecast.Current.Wind, "source": "Open-Meteo",
	}
	data, _ := json.MarshalIndent(value, "", "  ")
	return string(data), nil
}

func getJSON(ctx context.Context, client *http.Client, endpoint string, target any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	request.Header.Set("User-Agent", "EasyAgent/0.1")
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(io.LimitReader(response.Body, maxHTTPResult)).Decode(target)
}

func weatherText(code int) string {
	switch code {
	case 0:
		return "晴"
	case 1, 2:
		return "少云"
	case 3:
		return "阴"
	case 45, 48:
		return "雾"
	case 51, 53, 55, 56, 57:
		return "毛毛雨"
	case 61, 63, 65, 66, 67:
		return "雨"
	case 71, 73, 75, 77:
		return "雪"
	case 80, 81, 82:
		return "阵雨"
	case 85, 86:
		return "阵雪"
	case 95, 96, 99:
		return "雷暴"
	default:
		return fmt.Sprintf("WMO %d", code)
	}
}
