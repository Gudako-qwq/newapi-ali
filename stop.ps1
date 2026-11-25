$ErrorActionPreference = 'Stop'
$pidFile = Join-Path $PSScriptRoot '.local\server.pid'
$binary = Join-Path $PSScriptRoot 'bin\newapi-ali.exe'
if (-not (Test-Path -LiteralPath $pidFile)) { Write-Output '没有本项目的后台进程记录'; return }
$savedProcessId = 0
if (-not [int]::TryParse((Get-Content -LiteralPath $pidFile -Raw).Trim(), [ref]$savedProcessId)) { throw '进程记录格式不正确，未停止任何进程' }
$process = Get-Process -Id $savedProcessId -ErrorAction SilentlyContinue
if ($process) {
    if ($process.Path -ine $binary) { throw '进程路径不匹配，未停止任何进程' }
    Stop-Process -Id $savedProcessId
    $process.WaitForExit()
}
Remove-Item -LiteralPath $pidFile
Write-Output 'newapi-ali 已停止'
