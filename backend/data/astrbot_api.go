package data

// @Desc AstrBot 推送出口（统一推送出口 → QQ）
// -----------------------------------------------------------------------------------
// 本机 AstrBot 提供官方 Open API 用于主动发送消息：
//
//	POST {baseUrl}/api/v1/im/messages
//	Header: Authorization: Bearer <apiKey>     （服务端同时接受 X-API-Key，统一用 Bearer）
//	Body:   {"umo":"<platform_id>:<MessageType>:<session_id>","message":"<markdown>"}
//
// umo 形如「爱酱:FriendMessage:3C80967C94173B596EA1985D41040D25」——QQ 官方机器人
// 单聊的 session_id 是 openid，不是 QQ 号。成功响应为 HTTP 200 + {"status":"ok",...}。
// 该调用链已由本机 bilibili-notify 项目在生产环境验证可用。
//
// 地址口径：go-stock 跑在容器里，访问宿主机 AstrBot 用 http://host.docker.internal:6185；
// 若 go-stock 直接跑在宿主机则用 http://127.0.0.1:6185。

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"go-stock/backend/logger"

	"github.com/go-resty/resty/v2"
	"github.com/tidwall/gjson"
)

type AstrBotAPI struct {
	client *resty.Client
}

func NewAstrBotAPI() *AstrBotAPI {
	return &AstrBotAPI{
		client: SharedHTTPClient,
	}
}

// AstrBotMessageRequest /api/v1/im/messages 的请求体
type AstrBotMessageRequest struct {
	Umo     string `json:"umo"`
	Message string `json:"message"`
}

// SendToAstrBot 读取设置中的 AstrBot 配置并推送；标题作为一级标题拼在正文之前。
// 返回值面向调用方与设置页「发送测试通知」直接展示。
func (a AstrBotAPI) SendToAstrBot(title, message string) string {
	cfg := GetSettingConfig()
	if cfg == nil || !cfg.AstrBotPushEnable {
		return "AstrBot 推送未开启"
	}
	return a.SendToAstrBotByConfig(cfg.AstrBotBaseUrl, cfg.AstrBotApiKey, cfg.AstrBotUmo, title, message)
}

// SendToAstrBotByConfig 使用显式参数推送，供设置页「发送测试通知」复用（不读库配置）。
func (a AstrBotAPI) SendToAstrBotByConfig(baseUrl, apiKey, umo, title, message string) string {
	baseUrl = strings.TrimRight(strings.TrimSpace(baseUrl), "/")
	apiKey = strings.TrimSpace(apiKey)
	umo = strings.TrimSpace(umo)
	switch {
	case baseUrl == "":
		return "AstrBot 推送未配置服务地址"
	case apiKey == "":
		return "AstrBot 推送未配置 API Key"
	case umo == "":
		return "AstrBot 推送未配置会话标识(umo)"
	}

	text := strings.TrimSpace(message)
	if t := strings.TrimSpace(title); t != "" {
		text = "# " + t + "\n\n" + text
	}

	body, err := json.Marshal(AstrBotMessageRequest{Umo: umo, Message: text})
	if err != nil {
		logger.SugaredLogger.Errorf("AstrBot 消息构造失败: %s", err.Error())
		return "AstrBot 消息构造失败：" + err.Error()
	}

	resp, err := a.client.R().
		SetHeader("Authorization", "Bearer "+apiKey).
		SetHeader("Content-Type", "application/json").
		SetBody(body).
		Post(baseUrl + "/api/v1/im/messages")
	if err != nil {
		logger.SugaredLogger.Errorf("发送 AstrBot 消息失败: %s", err.Error())
		return "发送 AstrBot 消息失败：" + err.Error()
	}

	result := resp.String()
	logger.SugaredLogger.Infof("send astrbot message: status=%d body=%s", resp.StatusCode(), result)
	if resp.StatusCode() != http.StatusOK {
		return fmt.Sprintf("发送 AstrBot 消息失败：HTTP %d %s", resp.StatusCode(), result)
	}
	if status := gjson.Get(result, "status"); status.Exists() && status.String() != "ok" {
		return "发送 AstrBot 消息失败：" + result
	}
	return "发送 AstrBot 消息成功"
}
