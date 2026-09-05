// Package weixin implements the HTTP/JSON transport used by Tencent's WeChat
// ClawBot iLink channel. It is intentionally independent from either Agent
// runtime so the server can route inbound messages to EasyAgent or Codex.
package weixin

import (
	"bytes"
	"context"
	"crypto/aes"
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
	DefaultBaseURL    = "https://ilinkai.weixin.qq.com"
	DefaultCDNBaseURL = "https://novac2c.cdn.weixin.qq.com/c2c"
	defaultBotType    = "3"
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
	Type      int        `json:"type"`
	TextItem  *TextItem  `json:"text_item,omitempty"`
	ImageItem *ImageItem `json:"image_item,omitempty"`
	VoiceItem *VoiceItem `json:"voice_item,omitempty"`
	FileItem  *FileItem  `json:"file_item,omitempty"`
	VideoItem *VideoItem `json:"video_item,omitempty"`
}

type TextItem struct {
	Text string `json:"text"`
}

type CDNMedia struct {
	EncryptedQuery string `json:"encrypt_query_param,omitempty"`
	AESKey         string `json:"aes_key,omitempty"`
	FullURL        string `json:"full_url,omitempty"`
}

type ImageItem struct {
	Media  *CDNMedia `json:"media,omitempty"`
	AESKey string    `json:"aeskey,omitempty"`
}

type VoiceItem struct {
	Media         *CDNMedia `json:"media,omitempty"`
	EncodeType    int       `json:"encode_type,omitempty"`
	BitsPerSample int       `json:"bits_per_sample,omitempty"`
	SampleRate    int       `json:"sample_rate,omitempty"`
	PlaytimeMS    int       `json:"playtime,omitempty"`
	Text          string    `json:"text,omitempty"`
}

type FileItem struct {
	Media    *CDNMedia `json:"media,omitempty"`
	FileName string    `json:"file_name,omitempty"`
}

type VideoItem struct {
	Media *CDNMedia `json:"media,omitempty"`
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
	DownloadMedia(context.Context, CDNMedia, string) ([]byte, error)
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

const maxDownloadedMediaBytes = 10 * 1024 * 1024

// DownloadMedia follows Tencent's iLink CDN contract. Direct URLs are limited
// to Tencent HTTPS hosts so a forged update cannot turn EasyAgent into an SSRF
// proxy. The channel uses AES-128-ECB with PKCS#7 padding.
func (gateway *HTTPGateway) DownloadMedia(ctx context.Context, media CDNMedia, aesKey string) ([]byte, error) {
	downloadURL, err := mediaDownloadURL(media)
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, downloadURL, nil)
	if err != nil {
		return nil, err
	}
	client := *gateway.client
	previousRedirect := client.CheckRedirect
	client.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if _, err := mediaDownloadURL(CDNMedia{FullURL: next.URL.String()}); err != nil {
			return err
		}
		if previousRedirect != nil {
			return previousRedirect(next, via)
		}
		if len(via) >= 10 {
			return errors.New("微信媒体重定向次数过多")
		}
		return nil
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, fmt.Errorf("下载微信媒体: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("微信 CDN 返回 HTTP %d", response.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxDownloadedMediaBytes+1))
	if err != nil {
		return nil, fmt.Errorf("读取微信媒体: %w", err)
	}
	if len(data) > maxDownloadedMediaBytes {
		return nil, errors.New("微信媒体超过 10 MiB")
	}
	if strings.TrimSpace(aesKey) == "" {
		return data, nil
	}
	return decryptMedia(data, aesKey)
}

func mediaDownloadURL(media CDNMedia) (string, error) {
	if value := strings.TrimSpace(media.FullURL); value != "" {
		parsed, err := url.Parse(value)
		if err != nil || parsed == nil {
			return "", errors.New("微信媒体下载地址不可信")
		}
		host := strings.ToLower(parsed.Hostname())
		trustedHost := host == "qq.com" || strings.HasSuffix(host, ".qq.com") || host == "weixin.qq.com" || strings.HasSuffix(host, ".weixin.qq.com")
		if parsed.Scheme != "https" || !trustedHost {
			return "", errors.New("微信媒体下载地址不可信")
		}
		return parsed.String(), nil
	}
	if strings.TrimSpace(media.EncryptedQuery) == "" {
		return "", errors.New("微信媒体缺少下载引用")
	}
	return DefaultCDNBaseURL + "/download?encrypted_query_param=" + url.QueryEscape(media.EncryptedQuery), nil
}

func decryptMedia(ciphertext []byte, encodedKey string) ([]byte, error) {
	key, err := decodeMediaKey(encodedKey)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) == 0 || len(ciphertext)%block.BlockSize() != 0 {
		return nil, errors.New("微信媒体密文长度无效")
	}
	plaintext := make([]byte, len(ciphertext))
	for offset := 0; offset < len(ciphertext); offset += block.BlockSize() {
		block.Decrypt(plaintext[offset:offset+block.BlockSize()], ciphertext[offset:offset+block.BlockSize()])
	}
	padding := int(plaintext[len(plaintext)-1])
	if padding == 0 || padding > block.BlockSize() || padding > len(plaintext) {
		return nil, errors.New("微信媒体填充无效")
	}
	for _, value := range plaintext[len(plaintext)-padding:] {
		if int(value) != padding {
			return nil, errors.New("微信媒体填充无效")
		}
	}
	return plaintext[:len(plaintext)-padding], nil
}

func decodeMediaKey(value string) ([]byte, error) {
	value = strings.TrimSpace(value)
	if len(value) == 32 {
		if decoded, err := hex.DecodeString(value); err == nil {
			return decoded, nil
		}
	}
	decoded, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		decoded, err = base64.RawStdEncoding.DecodeString(value)
	}
	if err != nil {
		return nil, errors.New("微信媒体 AES Key 编码无效")
	}
	if len(decoded) == 16 {
		return decoded, nil
	}
	if len(decoded) == 32 {
		if raw, decodeErr := hex.DecodeString(string(decoded)); decodeErr == nil {
			return raw, nil
		}
	}
	return nil, errors.New("微信媒体 AES Key 长度无效")
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
