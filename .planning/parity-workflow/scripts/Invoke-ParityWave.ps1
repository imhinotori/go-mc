[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string]$Manifest,

    [switch]$Execute,
    [string]$RepoRoot = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
    $RepoRoot = (& git rev-parse --show-toplevel).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($RepoRoot)) {
        throw 'Not inside a git repository.'
    }
}
$RepoRoot = [IO.Path]::GetFullPath($RepoRoot)

$manifestPath = if ([IO.Path]::IsPathRooted($Manifest)) {
    [IO.Path]::GetFullPath($Manifest)
}
else {
    [IO.Path]::GetFullPath((Join-Path $RepoRoot $Manifest))
}
if (-not (Test-Path -LiteralPath $manifestPath -PathType Leaf)) {
    throw "Manifest not found: $manifestPath"
}

$spec = Get-Content -Raw -LiteralPath $manifestPath | ConvertFrom-Json
$tasks = @($spec.tasks)
if ($tasks.Count -eq 0) {
    throw 'Manifest has no tasks.'
}
if ($tasks.Count -gt 3 -or [int]$spec.max_parallel -gt 3) {
    throw 'Parity waves are limited to three simultaneous workers.'
}

$worker = Join-Path $RepoRoot '.planning\parity-workflow\scripts\Invoke-ParityWorker.ps1'
if (-not (Test-Path -LiteralPath $worker -PathType Leaf)) {
    throw "Worker launcher not found: $worker"
}

$ids = @($tasks | ForEach-Object { [string]$_.id })
if (($ids | Sort-Object -Unique).Count -ne $ids.Count) {
    throw 'Task ids must be unique inside a wave.'
}
foreach ($task in $tasks) {
    if ([string]::IsNullOrWhiteSpace([string]$task.expected_output)) {
        throw "Task $($task.id) is missing expected_output."
    }
}

Write-Output "Wave: $($spec.wave)"
Write-Output "Base: $($spec.base_ref)"
Write-Output "Model: $($spec.model)"

if (-not $Execute) {
    foreach ($task in $tasks) {
        & $worker -TaskId $task.id -PromptFile $task.prompt -Mode Plan -Model $spec.model -BaseRef $spec.base_ref -ExpectedOutput $task.expected_output -RepoRoot $RepoRoot
    }
    exit 0
}

# Git worktree creation is deliberately serial to avoid repository lock contention.
foreach ($task in $tasks) {
    & $worker -TaskId $task.id -PromptFile $task.prompt -Mode Prepare -Model $spec.model -BaseRef $spec.base_ref -ExpectedOutput $task.expected_output -RepoRoot $RepoRoot
}

# OpenCode runs in parallel only after every worktree exists.
$hostExe = (Get-Process -Id $PID).Path
$processes = @()
foreach ($task in $tasks) {
    $args = @(
        '-NoProfile',
        '-File', $worker,
        '-TaskId', [string]$task.id,
        '-PromptFile', [string]$task.prompt,
        '-Mode', 'Run',
        '-Model', [string]$spec.model,
        '-BaseRef', [string]$spec.base_ref,
        '-ExpectedOutput', [string]$task.expected_output,
        '-RepoRoot', $RepoRoot
    )
    $processes += [pscustomobject]@{
        Task = [string]$task.id
        Process = Start-Process -FilePath $hostExe -ArgumentList $args -WindowStyle Hidden -PassThru
    }
}

$failed = @()
foreach ($entry in $processes) {
    $entry.Process.WaitForExit()
    $code = $entry.Process.ExitCode
    Write-Output "$($entry.Task): exit $code"
    if ($code -ne 0) {
        $failed += $entry.Task
    }
}

if ($failed.Count -gt 0) {
    throw "Wave completed with failed workers: $($failed -join ', ')"
}

Write-Output 'Wave workers completed. No changes were committed or integrated.'
