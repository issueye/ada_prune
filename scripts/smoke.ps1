# End-to-end smoke test for AdaProxy.
#
# Starts a fake Command Code upstream and the headless gateway on temporary
# ports and a throwaway data directory, then walks the whole flow:
#
#   provider -> credential -> model -> client key -> /v1 call -> logs -> usage
#
#   powershell -ExecutionPolicy Bypass -File scripts/smoke.ps1
#   powershell -ExecutionPolicy Bypass -File scripts/smoke.ps1 -Keep   # keep the data dir
#
# Exits non-zero on the first failed assertion.
param(
    [switch]$Keep,
    [int]$UpstreamPort = 4099,
    [int]$GatewayPort = 3099
)

$ErrorActionPreference = "Stop"
$root = Split-Path -Parent (Split-Path -Parent $MyInvocation.MyCommand.Path)
Set-Location $root

$dataDir = Join-Path ([System.IO.Path]::GetTempPath()) ("adaproxy-smoke-" + [Guid]::NewGuid().ToString("N").Substring(0, 8))
New-Item -ItemType Directory -Force $dataDir | Out-Null

$upstream = $null
$gateway = $null

function Wait-Http([string]$url, [int]$timeoutSec = 20) {
    $deadline = (Get-Date).AddSeconds($timeoutSec)
    while ((Get-Date) -lt $deadline) {
        try {
            $resp = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 2
            if ($resp.StatusCode -ge 200) { return $true }
        } catch { }
        Start-Sleep -Milliseconds 200
    }
    return $false
}

function Step([string]$label) {
    Write-Host ("  " + $label) -ForegroundColor DarkGray
}

try {
    Write-Host "AdaProxy smoke test" -ForegroundColor Cyan

    Step "building binaries"
    & go build -o (Join-Path $dataDir "adp-server.exe") ./cmd/adp-server
    if ($LASTEXITCODE -ne 0) { throw "build adp-server failed" }
    & go build -o (Join-Path $dataDir "mockupstream.exe") ./tools/mockupstream
    if ($LASTEXITCODE -ne 0) { throw "build mockupstream failed" }

    Step "starting fake upstream on :$UpstreamPort"
    $upstream = Start-Process -FilePath (Join-Path $dataDir "mockupstream.exe") `
        -ArgumentList "-addr", "127.0.0.1:$UpstreamPort" -PassThru -WindowStyle Hidden
    if (-not (Wait-Http "http://127.0.0.1:$UpstreamPort/provider/v1/models")) {
        throw "the fake upstream did not start"
    }

    Step "starting gateway on :$GatewayPort"
    $env:ADP_DATA_DIR = $dataDir
    $env:ADP_PORT = "$GatewayPort"
    $gateway = Start-Process -FilePath (Join-Path $dataDir "adp-server.exe") `
        -PassThru -WindowStyle Hidden -RedirectStandardError (Join-Path $dataDir "server.err.log")
    if (-not (Wait-Http "http://127.0.0.1:$GatewayPort/health")) {
        throw "the gateway did not start; see $dataDir/server.err.log"
    }

    $base = "http://127.0.0.1:$GatewayPort"
    $admin = "$base/admin/api"

    Step "creating provider"
    $provider = Invoke-RestMethod -Uri "$admin/providers" -Method Post -ContentType "application/json" -Body (@{
        name    = "Mock Command Code"
        kind    = "command_code"
        baseUrl = "http://127.0.0.1:$UpstreamPort"
    } | ConvertTo-Json)

    Step "creating credential"
    $null = Invoke-RestMethod -Uri "$admin/credentials" -Method Post -ContentType "application/json" -Body (@{
        providerId = $provider.id
        name       = "main"
        secret     = "user_smoke_0123456789abcdef0123"
    } | ConvertTo-Json)

    Step "creating model"
    $model = Invoke-RestMethod -Uri "$admin/models" -Method Post -ContentType "application/json" -Body (@{
        providerId       = $provider.id
        displayName      = "Mock Model"
        publicName       = "mock-model"
        upstreamName     = "mock/mock-model"
        upstreamProtocol = "vendor_native"
        inputPrice       = 0.5
        outputPrice      = 1.5
    } | ConvertTo-Json)
    if (-not $model.routable) { throw "the model is not routable after adding a credential" }

    Step "creating client key"
    $key = Invoke-RestMethod -Uri "$admin/client-keys" -Method Post -ContentType "application/json" -Body (@{
        name = "smoke"
    } | ConvertTo-Json)
    if (-not $key.token.StartsWith("sk-adp-")) { throw "the client key does not have the expected prefix" }

    Step "calling /v1/chat/completions (non-streaming)"
    $answer = Invoke-RestMethod -Uri "$base/v1/chat/completions" -Method Post `
        -Headers @{ Authorization = "Bearer $($key.token)" } -ContentType "application/json" -Body (@{
        model    = "mock-model"
        messages = @(@{ role = "user"; content = "ping" })
    } | ConvertTo-Json -Depth 5)
    $text = $answer.choices[0].message.content
    if ($text -ne "hello from the mock upstream") { throw "unexpected answer: '$text'" }
    if ($answer.usage.prompt_tokens -ne 12) { throw "unexpected usage: $($answer.usage | ConvertTo-Json -Compress)" }

    Step "calling /v1/messages (Anthropic inbound, converted)"
    $anthropic = Invoke-RestMethod -Uri "$base/v1/messages" -Method Post `
        -Headers @{ Authorization = "Bearer $($key.token)" } -ContentType "application/json" -Body (@{
        model      = "mock-model"
        max_tokens = 64
        messages   = @(@{ role = "user"; content = "ping" })
    } | ConvertTo-Json -Depth 5)
    if ($anthropic.type -ne "message") { throw "expected an Anthropic message, got '$($anthropic.type)'" }
    if ($anthropic.content[0].text -ne "hello from the mock upstream") { throw "the converted answer is wrong" }

    Step "calling /v1/chat/completions (streaming)"
    $stream = Invoke-WebRequest -Uri "$base/v1/chat/completions" -Method Post `
        -Headers @{ Authorization = "Bearer $($key.token)" } -ContentType "application/json" -Body (@{
        model    = "mock-model"
        stream   = $true
        messages = @(@{ role = "user"; content = "ping" })
    } | ConvertTo-Json -Depth 5) -UseBasicParsing
    if ($stream.Content -notmatch "data: \[DONE\]") { throw "the stream has no terminator" }

    Step "checking the rejection path"
    try {
        $null = Invoke-RestMethod -Uri "$base/v1/chat/completions" -Method Post -ContentType "application/json" -Body (@{
            model = "mock-model"; messages = @(@{ role = "user"; content = "ping" })
        } | ConvertTo-Json -Depth 5)
        throw "an unauthenticated call was accepted"
    } catch {
        if ($_.Exception.Response.StatusCode.value__ -ne 401) { throw "expected 401, got $($_.Exception.Response.StatusCode.value__)" }
    }

    Step "reading logs and usage"
    Start-Sleep -Milliseconds 400
    $logs = Invoke-RestMethod -Uri "$admin/logs?limit=10"
    if ($logs.total -lt 3) { throw "expected at least 3 recorded calls, got $($logs.total)" }
    $usage = Invoke-RestMethod -Uri "$admin/usage?groupBy=model"
    if ($usage.summary.requests -lt 3) { throw "usage did not aggregate the calls" }

    Write-Host ""
    Write-Host "  OK  three protocol paths, auth, logs and usage all behave" -ForegroundColor Green
    Write-Host ("  data: {0}" -f $dataDir) -ForegroundColor DarkGray
}
finally {
    if ($gateway) { Stop-Process -Id $gateway.Id -Force -ErrorAction SilentlyContinue }
    if ($upstream) { Stop-Process -Id $upstream.Id -Force -ErrorAction SilentlyContinue }
    Remove-Item Env:\ADP_DATA_DIR, Env:\ADP_PORT -ErrorAction SilentlyContinue
    if (-not $Keep) {
        Start-Sleep -Milliseconds 300
        Remove-Item -Recurse -Force $dataDir -ErrorAction SilentlyContinue
    }
}
