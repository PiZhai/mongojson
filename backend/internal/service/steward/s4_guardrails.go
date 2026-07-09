package steward

import "strings"

type advisorUnsafePattern struct {
	category string
	phrase   string
}

var advisorUnsafePatterns = []advisorUnsafePattern{
	{category: "external_send", phrase: "send email"},
	{category: "external_send", phrase: "send message"},
	{category: "external_send", phrase: "reply to"},
	{category: "external_send", phrase: "post to"},
	{category: "external_send", phrase: "publish"},
	{category: "external_send", phrase: "发送邮件"},
	{category: "external_send", phrase: "发邮件"},
	{category: "external_send", phrase: "发送消息"},
	{category: "external_send", phrase: "回复消息"},
	{category: "external_send", phrase: "发短信"},
	{category: "external_send", phrase: "通知对方"},
	{category: "destructive", phrase: "delete file"},
	{category: "destructive", phrase: "delete all"},
	{category: "destructive", phrase: "rm -rf"},
	{category: "destructive", phrase: "drop table"},
	{category: "destructive", phrase: "drop database"},
	{category: "destructive", phrase: "format disk"},
	{category: "destructive", phrase: "删除文件"},
	{category: "destructive", phrase: "清空"},
	{category: "destructive", phrase: "格式化"},
	{category: "financial", phrase: "pay invoice"},
	{category: "financial", phrase: "payment"},
	{category: "financial", phrase: "transfer money"},
	{category: "financial", phrase: "place order"},
	{category: "financial", phrase: "付款"},
	{category: "financial", phrase: "支付"},
	{category: "financial", phrase: "转账"},
	{category: "financial", phrase: "下单"},
	{category: "credential", phrase: "password"},
	{category: "credential", phrase: "api key"},
	{category: "credential", phrase: "secret key"},
	{category: "credential", phrase: "private key"},
	{category: "credential", phrase: "cookie"},
	{category: "credential", phrase: "读取凭据"},
	{category: "credential", phrase: "读取密码"},
	{category: "credential", phrase: "私钥"},
	{category: "credential", phrase: "密钥"},
	{category: "system_config", phrase: "sudo"},
	{category: "system_config", phrase: "registry"},
	{category: "system_config", phrase: "firewall"},
	{category: "system_config", phrase: "system settings"},
	{category: "system_config", phrase: "修改系统配置"},
	{category: "system_config", phrase: "注册表"},
	{category: "system_config", phrase: "防火墙"},
	{category: "code_submit", phrase: "git push"},
	{category: "code_submit", phrase: "deploy to production"},
	{category: "code_submit", phrase: "submit form"},
	{category: "code_submit", phrase: "提交代码"},
	{category: "code_submit", phrase: "推送代码"},
	{category: "code_submit", phrase: "上线发布"},
}

func advisorSuggestionSafetyViolation(suggestion AutonomyAdvisorSuggestion) string {
	fields := []string{
		suggestion.Title,
		suggestion.Summary,
		suggestion.TriggerReason,
		suggestion.SuggestedAction,
		suggestion.ImpactSummary,
	}
	haystack := strings.ToLower(strings.Join(fields, "\n"))
	for _, pattern := range advisorUnsafePatterns {
		phrase := strings.ToLower(strings.TrimSpace(pattern.phrase))
		if phrase != "" && strings.Contains(haystack, phrase) {
			return pattern.category + ":" + pattern.phrase
		}
	}
	return ""
}
