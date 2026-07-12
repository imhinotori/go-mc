[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [ValidatePattern('^[a-z0-9][a-z0-9-]{1,62}$')]
    [string]$TaskId,

    [Parameter(Mandatory)]
    [string]$PromptFile,

    [ValidateSet('Plan', 'Prepare', 'Run', 'All')]
    [string]$Mode = 'Plan',

    [string]$Model = 'minimax/MiniMax-M3',
    [string]$BaseRef = 'HEAD',
    [string]$ExpectedOutput = '',
    [string]$RepoRoot = '',
    [string]$WorktreeRoot = ''
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

function Invoke-Git {
    param([Parameter(ValueFromRemainingArguments)][string[]]$Arguments)
    $previousErrorAction = $ErrorActionPreference
    try {
        # Windows PowerShell wraps progress written by native programs to stderr as a
        # NativeCommandError. Git uses stderr for normal progress, so only its exit code
        # is authoritative here.
        $ErrorActionPreference = 'Continue'
        $output = @(& git -C $RepoRoot @Arguments 2>&1)
        $exitCode = $LASTEXITCODE
    }
    finally {
        $ErrorActionPreference = $previousErrorAction
    }
    $output | Write-Output
    if ($exitCode -ne 0) {
        throw "git failed: git -C $RepoRoot $($Arguments -join ' ')"
    }
}

if ([string]::IsNullOrWhiteSpace($RepoRoot)) {
    $RepoRoot = (& git rev-parse --show-toplevel).Trim()
    if ($LASTEXITCODE -ne 0 -or [string]::IsNullOrWhiteSpace($RepoRoot)) {
        throw 'Not inside a git repository.'
    }
}
$RepoRoot = [IO.Path]::GetFullPath($RepoRoot)

if ([IO.Path]::IsPathRooted($PromptFile)) {
    throw 'PromptFile must be repository-relative so the same path exists inside the worker worktree.'
}

if ([string]::IsNullOrWhiteSpace($WorktreeRoot)) {
    $repoName = Split-Path $RepoRoot -Leaf
    $WorktreeRoot = Join-Path (Split-Path $RepoRoot -Parent) "$repoName-parity-worktrees"
}
$WorktreeRoot = [IO.Path]::GetFullPath($WorktreeRoot)
$worktree = Join-Path $WorktreeRoot $TaskId
$branch = "codex/parity-$TaskId"
$configDir = Join-Path $worktree '.planning\parity-workflow\opencode'
$promptInWorktree = Join-Path $worktree ($PromptFile -replace '/', '\')
$runRoot = Join-Path $RepoRoot ".planning\parity-workflow\runs\$TaskId"
$expectedOutputInWorktree = ''
if (-not [string]::IsNullOrWhiteSpace($ExpectedOutput)) {
    if ([IO.Path]::IsPathRooted($ExpectedOutput)) {
        throw 'ExpectedOutput must be repository-relative.'
    }
    $expectedOutputInWorktree = Join-Path $worktree ($ExpectedOutput -replace '/', '\')
}

$plan = [ordered]@{
    task_id = $TaskId
    mode = $Mode
    model = $Model
    base_ref = $BaseRef
    branch = $branch
    worktree = $worktree
    prompt = $promptInWorktree
    config_dir = $configDir
    logs = $runRoot
    expected_output = $expectedOutputInWorktree
}
$plan | ConvertTo-Json | Write-Output

if ($Mode -eq 'Plan') {
    return
}

if ($Mode -in @('Prepare', 'All')) {
    # Tracked changes can make the base ambiguous and are a hard stop. Untracked runtime artifacts
    # (world data, logs, audit dumps) do not enter a new worktree and are preserved with a warning.
    $dirty = @(& git -C $RepoRoot status --porcelain --untracked-files=no)
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to inspect repository status.'
    }
    if ($dirty.Count -gt 0) {
        throw "Main repository has tracked changes; commit or resolve them before preparing workers.`n$($dirty -join "`n")"
    }
    $untracked = @(& git -C $RepoRoot status --porcelain --untracked-files=normal)
    if ($LASTEXITCODE -ne 0) {
        throw 'Unable to inspect untracked repository paths.'
    }
    if ($untracked.Count -gt 0) {
        Write-Warning "Untracked paths will be preserved and excluded from worker worktrees:`n$($untracked -join "`n")"
    }
    if (Test-Path -LiteralPath $worktree) {
        $existingRoot = (& git -C $worktree rev-parse --show-toplevel).Trim()
        $existingBranch = (& git -C $worktree branch --show-current).Trim()
        if ($LASTEXITCODE -ne 0 -or [IO.Path]::GetFullPath($existingRoot) -ne [IO.Path]::GetFullPath($worktree) -or $existingBranch -ne $branch) {
            throw "Existing worktree does not match the requested task: $worktree"
        }
        Write-Output "Reusing prepared worktree: $worktree"
    }
    else {
        & git -C $RepoRoot show-ref --verify --quiet "refs/heads/$branch"
        if ($LASTEXITCODE -eq 0) {
            throw "Branch exists without its expected worktree: $branch"
        }
        New-Item -ItemType Directory -Force -Path $WorktreeRoot | Out-Null
        Invoke-Git worktree add -b $branch $worktree $BaseRef
    }
    if (-not (Test-Path -LiteralPath $promptInWorktree -PathType Leaf)) {
        throw "Prompt is not present in the prepared worktree. Commit the workflow first: $promptInWorktree"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $configDir 'opencode.json') -PathType Leaf)) {
        throw "OpenCode config is not present in the worktree: $configDir"
    }
    if ($Mode -eq 'Prepare') {
        return
    }
}

if ($Mode -in @('Run', 'All')) {
    if (-not (Get-Command opencode -ErrorAction SilentlyContinue)) {
        throw 'OpenCode is not installed or not on PATH.'
    }
    if (-not (Test-Path -LiteralPath $worktree -PathType Container)) {
        throw "Prepared worktree does not exist: $worktree"
    }
    if (-not (Test-Path -LiteralPath $promptInWorktree -PathType Leaf)) {
        throw "Prompt does not exist: $promptInWorktree"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $configDir 'opencode.json') -PathType Leaf)) {
        throw "OpenCode config does not exist: $configDir"
    }

    New-Item -ItemType Directory -Force -Path $runRoot | Out-Null
    $stamp = Get-Date -Format 'yyyyMMdd-HHmmss'
    $stdout = Join-Path $runRoot "$stamp.jsonl"
    $stderr = Join-Path $runRoot "$stamp.stderr.log"
    $metadata = Join-Path $runRoot "$stamp.metadata.json"
    $prompt = Get-Content -Raw -LiteralPath $promptInWorktree
    $meta = [ordered]@{
        task_id = $TaskId
        started_at = (Get-Date).ToString('o')
        model = $Model
        base_ref = $BaseRef
        branch = $branch
        worktree = $worktree
        prompt_file = $PromptFile
        opencode_version = (& opencode --version).Trim()
    }
    $meta | ConvertTo-Json | Set-Content -LiteralPath $metadata -Encoding utf8

    $previousConfigDir = $env:OPENCODE_CONFIG_DIR
    try {
        $env:OPENCODE_CONFIG_DIR = $configDir
        Push-Location $worktree
        try {
            & opencode run --dir $worktree --model $Model --format json --auto --title "parity-$TaskId" $prompt 1> $stdout 2> $stderr
            $exitCode = $LASTEXITCODE
        }
        finally {
            Pop-Location
        }
    }
    finally {
        $env:OPENCODE_CONFIG_DIR = $previousConfigDir
    }

    Write-Output "OpenCode exit code: $exitCode"
    Write-Output "JSONL: $stdout"
    Write-Output "stderr: $stderr"
    & git -C $worktree status --short
    & git -C $worktree diff --stat

    if ($exitCode -ne 0) {
        throw "OpenCode worker failed with exit code $exitCode. Inspect $stderr"
    }
    if ([string]::IsNullOrWhiteSpace($expectedOutputInWorktree) -or -not (Test-Path -LiteralPath $expectedOutputInWorktree -PathType Leaf)) {
        throw "OpenCode returned success without the required artifact: $expectedOutputInWorktree"
    }
    if ((Get-Item -LiteralPath $expectedOutputInWorktree).Length -eq 0) {
        throw "OpenCode produced an empty required artifact: $expectedOutputInWorktree"
    }
    $expectedGitPath = ($ExpectedOutput -replace '\', '/')
    $unexpected = @(& git -C $worktree status --porcelain --untracked-files=all | Where-Object {
        $changedPath = $_.Substring(3).Trim('"') -replace '\', '/'
        $changedPath -ne $expectedGitPath
    })
    if ($unexpected.Count -gt 0) {
        throw "Worker changed files outside ExpectedOutput '$ExpectedOutput':`n$($unexpected -join "`n")"
    }
}
