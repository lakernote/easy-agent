// Package weixin implements the HTTP/JSON transport used by Tencent's WeChat
// ClawBot iLink channel. It is intentionally independent from either Agent
// runtime so the server can route inbound messages to EasyAgent or Codex.
package weixin

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL = "https://ilinkai.weixin.qq.com"
	defaultBotType = "3"
	// iLinkClientVersion follows Tencent/openclaw-weixin 2.4.6:
	// major<<16 | minor<<8 | patch.
	iLinkClientVersion = 2<<16 | 4<<8 | 6
	channelVersion     = "2.4.6"
)

type Account struct {
	ID      string
	Token   string
	BaseURL string
}

type QRStart struct {
	Code    string
	Content string
}

type QRStatus struct {
	Status       string `json:"status"`
	BotToken     string `json:"bot_token"`
	AccountID    string `json:"ilink_bot_id"`
	BaseURL      string `json:"baseurl"`
	UserID       string `json:"ilink_user_id"`
	RedirectHost string `json:"redirect_host"`
}

type MessageItem struct {
	Type     int       `json:"type"`
	TextItem *TextItem `json:"text_item,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}

type Message struct {
	Sequence     int64         `json:"seq,omitempty"`
	MessageID    int64         `json:"message_id,omitempty"`
	FromUserID   string        `json:"from_user_id,omitempty"`
	ToUserID     string        `json:"to_user_id,omitempty"`
	ClientID     string        `json:"client_id,omitempty"`
	CreatedAtMS  int64         `json:"create_time_ms,omitempty"`
	SessionID    string        `json:"session_id,omitempty"`
	MessageType  int           `json:"message_type,omitempty"`
	MessageState int           `json:"message_state,omitempty"`
	Items        []MessageItem `json:"item_list,omitempty"`
	ContextToken string        `json:"context_token,omitempty"`
	RunID        string        `json:"run_id,omitempty"`
}

type Updates struct {
	ReturnCode        int       `json:"ret,omitempty"`
	ErrorCode         int       `json:"errcode,omitempty"`
	ErrorMessage      string    `json:"errmsg,omitempty"`
	Messages          []Message `json:"msgs,omitempty"`
	Buffer            string    `json:"get_updates_buf,omitempty"`
	LongPollTimeoutMS int       `json:"longpolling_timeout_ms,omitempty"`
}

type Gateway interface {
	StartLogin(context.Context, []string) (QRStart, error)
	PollLogin(context.Context, string, string, string) (QRStatus, error)
	GetUpdates(context.Context, Account, string) (Updates, error)
	SendText(context.Context, Account, string, string, string) error
}

type HTTPGateway struct {
	client *http.Client
}

func NewHTTPGateway(client *http.Client) *HTTPGateway {
	if client == nil {
		client = &http.Client{Timeout: 45 * time.Second}
	}
	return &HTTPGateway{client: client}
}

func (gateway *HTTPGateway) StartLogin(ctx context.Context, localTokens []string) (QRStart, error) {
	var response struct {
		Code    string `json:"qrcode"`
		Content string `json:"qrcode_img_content"`
	}
	endpoint := "ilink/bot/get_bot_qrcode?bot_type=" + url.QueryEscape(defaultBotType)
	if err := gateway.request(ctx, http.MethodPost, DefaultBaseURL, endpoint, "", map[string]any{"local_token_list": localTokens}, &response, false); err != nil {
		return QRStart{}, err
	}
	if strings.TrimSpace(response.Code) == "" || strings.TrimSpace(response.Content) == "" {
		return QRStart{}, errors.New("微信未返回有效二维码")
	}
	return QRStart{Code: response.Code, Content: response.Content}, nil
}

func (gateway *HTTPGateway) PollLogin(ctx context.Context, baseURL, code, verifyCode string) (QRStatus, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	endpoint := "ilink/bot/get_qrcode_status?qrcode=" + url.QueryEscape(code)
	if strings.TrimSpace(verifyCode) != "" {
		endpoint += "&verify_code=" + url.QueryEscape(strings.TrimSpace(verifyCode))
	}
	var response QRStatus
	if err := gateway.request(ctx, http.MethodGet, baseURL, endpoint, "", nil, &response, false); err != nil {
		return QRStatus{}, err
	}
	return response, nil
}

func (gateway *HTTPGateway) GetUpdates(ctx context.Context, account Account, buffer string) (Updates, error) {
	var response Updates
	body := map[string]any{"get_updates_buf": buffer, "base_info": baseInfo()}
	if err := gateway.request(ctx, http.MethodPost, account.BaseURL, "ilink/bot/getupdates", account.Token, body, &response, true); err != nil {
		return Updates{}, err
	}
	if response.ReturnCode != 0 || response.ErrorCode != 0 {
		return response, fmt.Errorf("微信拉取消息失败: ret=%d errcode=%d %s", response.ReturnCode, response.ErrorCode, response.ErrorMessage)
	}
	return response, nil
}

func (gateway *HTTPGateway) SendText(ctx context.Context, account Account, userID, contextToken, text string) error {
	clientID, err := randomID()
	if err != nil {
		return err
	}
	body := map[string]any{
		"msg": Message{
			ToUserID: userID, ClientID: clientID, MessageType: 2, MessageState: 2,
			Items:        []MessageItem{{Type: 1, TextItem: &TextItem{Text: text}}},
			ContextToken: contextToken,
		},
		"base_info": baseInfo(),
	}
	var response struct {
		ReturnCode   int    `json:"ret,omitempty"`
		ErrorMessage string `json:"errmsg,omitempty"`
	}
	if err := gateway.request(ctx, http.MethodPost, account.BaseURL, "ilink/bot/sendmessage", account.Token, body, &response, true); err != nil {
		return err
	}
	if response.ReturnCode != 0 {
		return fmt.Errorf("微信发送消息失败: ret=%d %s", response.ReturnCode, response.ErrorMessage)
	}
	return nil
}

func (gateway *HTTPGateway) request(ctx context.Context, method, baseURL, endpoint, token string, body any, output any, authenticated bool) error {
	requestURL, err := resolveURL(baseURL, endpoint)
	if err != nil {
		return err
	}
	var reader io.Reader
	if body != nil {
		data, marshalErr := json.Marshal(body)
		if marshalErr != nil {
			return marshalErr
		}
		reader = bytes.NewReader(data)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, reader)
	if err != nil {
		return err
	}
	request.Header.Set("iLink-App-Id", "bot")
	request.Header.Set("iLink-App-ClientVersion", strconv.Itoa(iLinkClientVersion))
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if authenticated {
		if strings.TrimSpace(token) == "" {
			return errors.New("微信绑定凭据为空")
		}
		uin, uinErr := randomUIN()
		if uinErr != nil {
			return uinErr
		}
		request.Header.Set("AuthorizationType", "ilink_bot_token")
		request.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
		request.Header.Set("X-WECHAT-UIN", uin)
	}
	response, err := gateway.client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	data, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("微信接口 HTTP %d: %s", response.StatusCode, strings.TrimSpace(string(data)))
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("解析微信接口响应: %w", err)
	}
	return nil
}

func resolveURL(baseURL, endpoint string) (string, error) {
	if strings.TrimSpace(baseURL) == "" {
		baseURL = DefaultBaseURL
	}
	base, err := url.Parse(strings.TrimRight(baseURL, "/") + "/")
	if err != nil || base.Scheme != "https" || base.Host == "" {
		return "", errors.New("微信接口地址无效")
	}
	reference, err := url.Parse(strings.TrimLeft(endpoint, "/"))
	if err != nil {
		return "", err
	}
	return base.ResolveReference(reference).String(), nil
}

func baseInfo() map[string]any {
	return map[string]any{"channel_version": channelVersion, "bot_agent": "EasyAgent/1"}
}

func randomUIN() (string, error) {
	data := make([]byte, 4)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	value := uint32(data[0])<<24 | uint32(data[1])<<16 | uint32(data[2])<<8 | uint32(data[3])
	return base64.StdEncoding.EncodeToString([]byte(strconv.FormatUint(uint64(value), 10))), nil
}

func randomID() (string, error) {
	data := make([]byte, 16)
	if _, err := rand.Read(data); err != nil {
		return "", err
	}
	return "easyagent-" + hex.EncodeToString(data), nil
}
