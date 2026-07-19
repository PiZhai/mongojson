package steward

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

type advisorHTTPError struct {
	Operation   string
	StatusCode  int
	Status      string
	ProviderMsg string
	RetryAfter  time.Duration
}

func (e *advisorHTTPError) Error() string {
	if e == nil {
		return "model provider request failed"
	}
	message := strings.TrimSpace(e.ProviderMsg)
	if message == "" {
		message = "provider returned an empty error response"
	}
	return fmt.Sprintf("%s failed with %s: %s", defaultString(e.Operation, "advisor request"), e.Status, message)
}

func newAdvisorHTTPError(operation, status string, statusCode int, body []byte, retryAfter ...string) error {
	delay := time.Duration(0)
	if len(retryAfter) > 0 {
		delay = parseAdvisorRetryAfter(retryAfter[0])
	}
	return &advisorHTTPError{
		Operation:   operation,
		StatusCode:  statusCode,
		Status:      status,
		ProviderMsg: advisorProviderErrorMessage(body),
		RetryAfter:  delay,
	}
}

func parseAdvisorRetryAfter(value string) time.Duration {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	if retryAt, err := http.ParseTime(value); err == nil {
		if delay := time.Until(retryAt); delay > 0 {
			return delay
		}
	}
	return 0
}

func advisorProviderErrorMessage(body []byte) string {
	var envelope struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
			Code    any    `json:"code"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && strings.TrimSpace(envelope.Error.Message) != "" {
		parts := []string{strings.TrimSpace(envelope.Error.Message)}
		if envelope.Error.Type != "" {
			parts = append(parts, "type="+envelope.Error.Type)
		}
		if envelope.Error.Code != nil {
			parts = append(parts, fmt.Sprintf("code=%v", envelope.Error.Code))
		}
		return strings.Join(parts, "; ")
	}
	return strings.TrimSpace(string(body))
}

type AdvisorFailureDetails struct {
	Code             string   `json:"code"`
	Title            string   `json:"title"`
	Message          string   `json:"message"`
	Suggestions      []string `json:"suggestions"`
	Retryable        bool     `json:"retryable"`
	TechnicalSummary string   `json:"technical_summary,omitempty"`
}

func describeAdvisorFailure(cause error) AdvisorFailureDetails {
	technical := sanitizeAdvisorStatusError(cause)
	lower := strings.ToLower(technical)
	statusCode := 0
	var httpErr *advisorHTTPError
	if errors.As(cause, &httpErr) {
		statusCode = httpErr.StatusCode
		lower = strings.ToLower(httpErr.ProviderMsg)
		technical = sanitizeAdvisorStatusError(httpErr)
	}
	detail := AdvisorFailureDetails{
		Code:             "MODEL_UNKNOWN_ERROR",
		Title:            "模型请求发生未知错误",
		Message:          "系统没有获得可用的模型响应。",
		Suggestions:      []string{"稍后重试", "打开模型配置运行完整连接检查", "在全面检查中查看模型状态和最近错误"},
		Retryable:        true,
		TechnicalSummary: technical,
	}

	switch {
	case advisorContextSizeExceeded(cause):
		detail.Code = "MODEL_CONTEXT_TOO_LARGE"
		detail.Title = "模型上下文仍然过大"
		detail.Message = "系统已自动压缩较早回合并保留最近完整工具调用后重试，但模型服务仍拒绝了请求大小。"
		detail.Suggestions = []string{"点击继续再次从持久化工作状态恢复", "检查模型服务实际支持的上下文窗口", "如使用兼容网关，检查其请求体大小限制"}
		detail.Retryable = true
	case advisorRequestedUnknownTool(lower):
		detail.Code = "MODEL_TOOL_NOT_LOADED"
		detail.Title = "模型请求的工具尚未加载"
		detail.Message = "模型请求了当前轮次未注册的工具，系统已拒绝执行该调用；该工具可能尚未加载，也可能名称无效。"
		detail.Suggestions = []string{"先在工具库确认该工具确实存在、已启用且健康", "重新发送原消息，让系统加载已确认工具的完整定义", "若问题持续，运行完整协议检查并核对工具名称"}
		detail.Retryable = false
	case strings.Contains(lower, "tool schema") || strings.Contains(lower, "invalid schema for function") || strings.Contains(lower, "function parameters"):
		detail.Code = "MODEL_TOOL_SCHEMA_INVALID"
		detail.Title = "工具调用协议配置错误"
		detail.Message = "模型服务可以连接，但拒绝了系统发送的工具参数定义。"
		detail.Suggestions = []string{"运行模型配置中的“完整协议检查”定位具体工具", "修复或停用提示中指出的无效工具 Schema", "修复后重新发送原消息"}
		detail.Retryable = false
	case statusCode == 401 || strings.Contains(lower, "invalid api key") || strings.Contains(lower, "authentication") || strings.Contains(lower, "unauthorized"):
		detail.Code = "MODEL_AUTHENTICATION_FAILED"
		detail.Title = "模型认证失败"
		detail.Message = "模型服务拒绝了当前 API Key 或认证信息。"
		detail.Suggestions = []string{"检查 API Key 是否正确且未过期", "确认 API Key 属于当前接口地址", "保存配置后重新运行连接检查"}
		detail.Retryable = false
	case statusCode == 403 || strings.Contains(lower, "forbidden") || strings.Contains(lower, "permission denied"):
		detail.Code = "MODEL_ACCESS_DENIED"
		detail.Title = "模型访问被拒绝"
		detail.Message = "当前账号或 API Key 没有调用该模型的权限。"
		detail.Suggestions = []string{"确认账号已开通目标模型", "检查服务商余额、地区或组织限制", "改用当前账号有权访问的模型 ID"}
		detail.Retryable = false
	case statusCode == 404 || strings.Contains(lower, "model not found") || strings.Contains(lower, "does not exist"):
		detail.Code = "MODEL_NOT_FOUND"
		detail.Title = "模型名称不可用"
		detail.Message = "接口地址可访问，但服务商找不到当前模型 ID。"
		detail.Suggestions = []string{"从服务商控制台复制准确的模型 ID", "确认接口地址与模型属于同一服务商", "保存后重新运行连接检查"}
		detail.Retryable = false
	case statusCode == 429 || strings.Contains(lower, "rate limit") || strings.Contains(lower, "too many requests") || strings.Contains(lower, "quota"):
		detail.Code = "MODEL_RATE_LIMITED"
		detail.Title = "模型请求受限"
		detail.Message = "服务商触发了频率、并发或额度限制。"
		detail.Suggestions = []string{"等待服务商限制窗口结束后重试", "检查账户余额和调用配额", "减少并发任务或切换可用模型"}
		detail.Retryable = true
	case errors.Is(cause, context.DeadlineExceeded) || strings.Contains(lower, "deadline exceeded") || strings.Contains(lower, "timeout") || strings.Contains(lower, "timed out"):
		detail.Code = "MODEL_TIMEOUT"
		detail.Title = "模型响应超时"
		detail.Message = "模型在当前请求超时时间内没有返回完整响应。"
		detail.Suggestions = []string{"稍后重试", "在模型配置中适当增加请求超时", "检查模型服务负载或缩短过长的上下文"}
		detail.Retryable = true
	case advisorNetworkError(cause, lower):
		detail.Code = "MODEL_NETWORK_ERROR"
		detail.Title = "无法连接模型服务"
		detail.Message = "后端无法与配置的模型地址建立稳定连接。"
		detail.Suggestions = []string{"检查接口地址、DNS、代理和网络连接", "确认模型服务正在运行且后端账户可以访问", "恢复网络后运行完整连接检查"}
		detail.Retryable = true
	case statusCode >= 500:
		detail.Code = "MODEL_PROVIDER_UNAVAILABLE"
		detail.Title = "模型服务暂时异常"
		detail.Message = "模型服务商返回了服务器错误。"
		detail.Suggestions = []string{"稍后重试", "查看服务商状态页", "必要时临时切换模型或接口地址"}
		detail.Retryable = true
	case strings.Contains(lower, "circuit open"):
		detail.Code = "MODEL_CIRCUIT_OPEN"
		detail.Title = "模型保护性熔断中"
		detail.Message = "连续失败触发了短暂熔断，系统正在避免重复无效请求。"
		detail.Suggestions = []string{"先运行完整连接检查确认根因", "修复配置或服务问题后等待熔断窗口结束", "不要连续重复发送相同请求"}
		detail.Retryable = true
	case strings.Contains(lower, "reasoning_content") || strings.Contains(lower, "no choices") || strings.Contains(lower, "neither text nor tool calls") || strings.Contains(lower, "decode"):
		detail.Code = "MODEL_PROTOCOL_MISMATCH"
		detail.Title = "模型响应协议不兼容"
		detail.Message = "模型已响应，但返回格式不符合当前 Chat Completions 工具调用协议。"
		detail.Suggestions = []string{"确认服务完整兼容 OpenAI Chat Completions", "检查模型的 thinking/reasoning 模式要求", "切换兼容模型后运行完整协议检查"}
		detail.Retryable = false
	case statusCode == 400:
		detail.Code = "MODEL_REQUEST_REJECTED"
		detail.Title = "模型拒绝了请求参数"
		detail.Message = "模型服务可连接，但认为当前请求内容或参数无效。"
		detail.Suggestions = []string{"运行完整协议检查查看服务商原始原因", "确认模型支持 function calling", "检查接口地址和模型 ID 是否匹配"}
		detail.Retryable = false
	case strings.Contains(lower, "disabled") || strings.Contains(lower, "is required") || strings.Contains(lower, "base url"):
		detail.Code = "MODEL_CONFIGURATION_INVALID"
		detail.Title = "模型配置不完整"
		detail.Message = "当前模型配置无法用于对话。"
		detail.Suggestions = []string{"填写接口地址、模型 ID 和 API Key", "保存配置并确认立即生效", "运行完整连接检查"}
		detail.Retryable = false
	case errors.Is(cause, ErrAdvisorDataLevelDenied) || strings.Contains(lower, "policy denied"):
		detail.Code = "MODEL_POLICY_BLOCKED"
		detail.Title = "模型调用被本机策略阻止"
		detail.Message = "请求没有发送给模型。"
		detail.Suggestions = []string{"检查当前模型和执行设置", "确认服务已加载最新的所有者模式配置", "完成配置后重新发送原消息"}
		detail.Retryable = false
	}
	return detail
}

func advisorRequestedUnknownTool(lower string) bool {
	return strings.Contains(lower, "agent model requested unknown tool ") ||
		strings.Contains(lower, "conversation model requested unknown tool ")
}

func advisorNetworkError(cause error, lower string) bool {
	var netErr net.Error
	return errors.As(cause, &netErr) || strings.Contains(lower, "connection refused") ||
		strings.Contains(lower, "no such host") || strings.Contains(lower, "tls handshake") ||
		strings.Contains(lower, "connection reset") || strings.Contains(lower, "network is unreachable")
}

func conversationAdvisorFailureReply(cause error) string {
	detail := describeAdvisorFailure(cause)
	lines := []string{
		"模型请求未完成：" + detail.Title,
		"原因：" + detail.Message,
		"处理建议：",
	}
	for _, suggestion := range detail.Suggestions {
		lines = append(lines, "- "+suggestion)
	}
	lines = append(lines, "本次消息已保存在本地，且没有执行任何工具。", "错误代码："+detail.Code)
	return strings.Join(lines, "\n")
}
