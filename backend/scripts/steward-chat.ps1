param(
    [string]$BaseUrl = "http://127.0.0.1:18080/api",
    [string]$ConversationId = "",
    [string]$Title = "PowerShell 对话",
    [string]$Message = ""
)

$ErrorActionPreference = "Stop"
$BaseUrl = $BaseUrl.TrimEnd("/")

function New-StewardConversation {
    param([string]$ConversationTitle)

    $body = @{ title = $ConversationTitle } | ConvertTo-Json
    $response = Invoke-RestMethod -Method Post -Uri "$BaseUrl/steward/conversations" -ContentType "application/json" -Body $body
    return $response.conversation.id
}

function Get-StewardMessages {
    param([string]$Id)

    $response = Invoke-RestMethod -Method Get -Uri "$BaseUrl/steward/conversations/$Id/messages?limit=100"
    return @($response.messages)
}

if ([string]::IsNullOrWhiteSpace($ConversationId)) {
    $ConversationId = New-StewardConversation -ConversationTitle $Title
}

Write-Host "Steward PowerShell Chat" -ForegroundColor Cyan
Write-Host "对话 ID: $ConversationId"
Write-Host "命令: /new 新对话, /history 查看历史, /id 显示 ID, /exit 退出"

$oneShot = -not [string]::IsNullOrWhiteSpace($Message)

while ($true) {
    if ($oneShot) {
        $text = $Message
        Write-Host ("你: {0}" -f $text)
    }
    else {
        $text = Read-Host "你"
    }

    if ([string]::IsNullOrWhiteSpace($text)) {
        continue
    }

    switch ($text.Trim()) {
        "/exit" { return }
        "/id" {
            Write-Host $ConversationId
            continue
        }
        "/new" {
            $ConversationId = New-StewardConversation -ConversationTitle $Title
            Write-Host "已创建新对话: $ConversationId" -ForegroundColor Green
            continue
        }
        "/history" {
            foreach ($message in (Get-StewardMessages -Id $ConversationId)) {
                $speaker = if ($message.role -eq "user") { "你" } else { "管家" }
                Write-Host ("{0}: {1}" -f $speaker, $message.content)
            }
            continue
        }
    }

    try {
        $before = Get-StewardMessages -Id $ConversationId
        $payload = @{ content = $text; context_limit = 10 } | ConvertTo-Json
        $response = Invoke-RestMethod -Method Post -Uri "$BaseUrl/steward/conversations/$ConversationId/messages" -ContentType "application/json" -Body $payload
        Write-Host ("管家: {0}" -f $response.message.content) -ForegroundColor Green

        $executions = @($response.message.executions)
        if ($executions.Count -eq 0) {
            if ($oneShot) { return }
            continue
        }

        $initialMessageId = $response.message.id
        $deadline = (Get-Date).AddMinutes(5)
        while ((Get-Date) -lt $deadline) {
            Start-Sleep -Milliseconds 750
            $messages = Get-StewardMessages -Id $ConversationId
            $newAssistant = @($messages | Where-Object {
                $_.role -eq "assistant" -and $_.id -ne $initialMessageId -and
                ([datetime]$_.created_at) -ge ([datetime]$response.message.created_at)
            }) | Select-Object -Last 1
            if ($null -ne $newAssistant) {
                Write-Host ("管家: {0}" -f $newAssistant.content) -ForegroundColor Green
                break
            }

            $current = @($messages | Where-Object { $_.id -eq $initialMessageId }) | Select-Object -First 1
            $statuses = @($current.executions | ForEach-Object { $_.status })
            if ($statuses.Count -gt 0 -and @($statuses | Where-Object { $_ -notin @("succeeded", "failed", "cancelled", "blocked") }).Count -eq 0) {
                Write-Host ("执行状态: {0}" -f ($statuses -join ", ")) -ForegroundColor DarkYellow
                break
            }
        }

        if ($oneShot) { return }
    }
    catch {
        Write-Host ("请求失败: {0}" -f $_.Exception.Message) -ForegroundColor Red
        if ($oneShot) { exit 1 }
    }
}
