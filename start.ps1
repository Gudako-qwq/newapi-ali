param([string]$EnvFile = '.env', [switch]$Background)
$ErrorActionPreference = 'Stop'
Set-Location -LiteralPath $PSScriptRoot
if (Test-Path -LiteralPath $EnvFile) {
    foreach ($line in Get-Content -LiteralPath $EnvFile -Encoding utf8) {
        $trimmed = $line.Trim()
        if ($trimmed.Length -eq 0 -or $trimmed.StartsWith('#')) { continue }
        if ($trimmed -notmatch '^([A-Z][A-Z0-9_]*)=(.*)$') { throw '环境配置格式错误，应为 NAME=value' }
        $name = $Matches[1]
        $value = $Matches[2].Trim()
        if ($value.Length -ge 2 -and (($value.StartsWith('"') -and $value.EndsWith('"')) -or ($value.StartsWith("'") -and $value.EndsWith("'")))) { $value = $value.Substring(1, $value.Length - 2) }
        [Environment]::SetEnvironmentVariable($name, $value, 'Process')
    }
}
$binary = Join-Path $PSScriptRoot 'bin\newapi-ali.exe'
if (-not (Test-Path -LiteralPath $binary) -and -not (Test-Path -LiteralPath 'web\dist\index.html')) {
    Push-Location -LiteralPath (Join-Path $PSScriptRoot 'web')
    try {
        & bun install --frozen-lockfile
        if ($LASTEXITCODE -ne 0) { throw '前端依赖安装失败' }
        & bun run build
        if ($LASTEXITCODE -ne 0) { throw '前端构建失败' }
    } finally { Pop-Location }
}
if ($Background) {
    if (-not (Test-Path -LiteralPath $binary)) {
        & go build -trimpath -o $binary .
        if ($LASTEXITCODE -ne 0) { throw '项目构建失败' }
    }
    $runtime = Join-Path $PSScriptRoot '.local'
    if (-not (Test-Path -LiteralPath $runtime)) { New-Item -ItemType Directory -Path $runtime | Out-Null }
    $pidFile = Join-Path $runtime 'server.pid'
    if (Test-Path -LiteralPath $pidFile) {
        $savedProcessId = 0
        if ([int]::TryParse((Get-Content -LiteralPath $pidFile -Raw).Trim(), [ref]$savedProcessId)) {
            $existing = Get-Process -Id $savedProcessId -ErrorAction SilentlyContinue
            if ($existing -and $existing.Path -ieq $binary) {
                Write-Output "newapi-ali 已在运行，PID=$savedProcessId"
                return
            }
        }
    }
    $process = Start-Process -FilePath $binary -WorkingDirectory $PSScriptRoot -WindowStyle Hidden -RedirectStandardOutput (Join-Path $runtime 'server.stdout.log') -RedirectStandardError (Join-Path $runtime 'server.stderr.log') -PassThru
    Set-Content -LiteralPath $pidFile -Value $process.Id -Encoding ascii
    $originText = if ($env:PUBLIC_ORIGIN) { $env:PUBLIC_ORIGIN } else { 'http://127.0.0.1:3000' }
    $origin = [Uri]$originText
    if ($origin.IsLoopback -and $origin.Scheme -eq 'http') {
        $ready = $false
        for ($attempt = 0; $attempt -lt 40; $attempt++) {
            Start-Sleep -Milliseconds 200
            $process.Refresh()
            if ($process.HasExited) { throw '启动失败，请查看 .local/server.stderr.log' }
            try {
                $health = Invoke-RestMethod -Uri ($originText.TrimEnd('/') + '/healthz') -TimeoutSec 1
                if ($health.success -and $health.data.status -eq 'ok') { $ready = $true; break }
            } catch { }
        }
        if (-not $ready) { throw '进程已启动但健康检查超时，请查看 .local 下的运行日志' }
    }
    Write-Output "newapi-ali 已后台启动，PID=$($process.Id)，访问地址：$originText"
    return
}
if (Test-Path -LiteralPath $binary) {
    & $binary
} else {
    & go run .
}
exit $LASTEXITCODE
