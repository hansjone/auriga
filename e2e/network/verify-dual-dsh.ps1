param([string]$ExpectedHead, [switch]$CompileOnly, [switch]$Run, [string]$MavenCommand = '.\mvnw.cmd')
$ErrorActionPreference = 'Stop'
if (($CompileOnly -and $Run) -or (-not $CompileOnly -and -not $Run)) { throw 'Choose CompileOnly or Run' }
$root = (Get-Location).Path
if (-not (Test-Path -LiteralPath (Join-Path $root 'mvnw.cmd'))) { throw 'Run from worktree root' }
if ($ExpectedHead -notmatch '^[a-f0-9]{40}$' -or (& git rev-parse HEAD).Trim() -cne $ExpectedHead) { throw 'Exact HEAD required' }
if (& git status --porcelain --untracked-files=no) { throw 'Tracked/index must be clean' }
if (-not $env:AURIGA_PNPM_STORE) { throw 'Keep dispatched locked pnpm store environment' }
$output = Join-Path $root ('e2e/network/target/verify-dual-dsh-' + [Guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $output | Out-Null
$archive = Join-Path $output 'source.tar'
& git archive --format=tar --output=$archive $ExpectedHead
if ($LASTEXITCODE -ne 0) { throw 'Cannot archive exact HEAD' }
$utf8 = New-Object Text.UTF8Encoding($false)
$testPaths = @('e2e/network/source-dual-dsh.mjs','e2e/network/support/dual-proxy.mjs','e2e/network/support/dual-cli.mjs','e2e/network/support/dual-prepare.mjs','e2e/network/support/dual-observer/index.mjs','e2e/network/support/dual-observer/cordis.patch.yml','e2e/network/support/dual-observer/package.json','e2e/network/support/DualDshLogin.java','e2e/network/support/dual-team.go','e2e/network/support/dual-verdict.mjs','e2e/network/source-assembly.mjs','e2e/network/support/assembly-plugin.mjs','e2e/network/support/plugin-send.mjs')
$sourcePath = 'clients/dsh-plugin/src/network/durable/outbox.js'
$testPaths += @('e2e/network/support/native-process.mjs','e2e/network/support/native-install.mjs','e2e/network/support/assembly-process.mjs','e2e/network/support/dsh-runtime/package.json','e2e/network/support/dsh-runtime/package-lock.json')
$testPaths += @('services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaHost.java','services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaWeb.java','services/platform-app/src/test/java/auriga/iam/NetworkSourceJavaDatabase.java')
$testPaths += @('scripts/beta-ui/plugin-dsh.mjs','scripts/beta-ui/runner.mjs','e2e/network/host-install-cache.test.mjs')
$testPaths += @('e2e/network/support/dual-edge.mjs','e2e/network/support/dual-edge_test.go','e2e/network/support/send4-platform-origin_test.go','e2e/network/verify-dual-dsh-preflight.ps1')
$testPaths += @('e2e/network/dual-cli-check.mjs')
$testPaths += @('clients/dsh-plugin/src/local/routes.js','clients/dsh-plugin/src/local/diagnostic.js','clients/dsh-plugin/test/login-routes.test.js')
$testPaths += @('e2e/network/entry-check.mjs','e2e/network/support/DualDshEntryCheck.java')
$testPaths += @('e2e/network/support/dual-state.mjs','e2e/network/dual-cleanup.test.mjs','e2e/network/support/NodeOwnedCleanupProbe.java','clients/dsh-plugin/test/network/transport/support/nats-binary.js')
$testPaths += @('infra/ai-gateway/cmd/network-source-assembly/main.go','infra/ai-gateway/cmd/network-source-assembly/submit_tls.go','infra/ai-gateway/cmd/network-source-assembly/submit_tls_test.go','e2e/network/support/gateway-submit.mjs','e2e/network/support/assembly-mailbox.mjs','e2e/network/support/gateway-send.mjs','e2e/network/submit-client.test.mjs','e2e/network/gateway-send-mailbox.test.mjs')
$manifest = [ordered]@{head=$ExpectedHead; archiveSha256=(Get-FileHash -LiteralPath $archive -Algorithm SHA256).Hash; mode=$(if($Run){'Run'}else{'CompileOnly'}); variants=@(); complete=$false}
function Save-Manifest { [IO.File]::WriteAllText((Join-Path $output 'manifest.json'), ($manifest | ConvertTo-Json -Depth 20), $utf8) }
function Hashes([string]$Directory) { $result=[ordered]@{}; foreach($path in $testPaths){$result[$path]=(Get-FileHash -LiteralPath (Join-Path $Directory $path) -Algorithm SHA256).Hash}; return $result }
function Recorded([string]$Name,[string]$Command,[scriptblock]$Action) {
  $log=Join-Path $output ($Name+'.log'); $saved=$ErrorActionPreference; $ErrorActionPreference='Continue'
  try { & $Action *> $log; $code=$LASTEXITCODE } finally {$ErrorActionPreference=$saved}
  if($null -eq $code){throw "Missing native exit for $Name"}
  $entry.commands += [ordered]@{command=$Command;exit=[int]$code;log=$log}; Save-Manifest; return [int]$code
}
try {
  foreach($mode in @('Control','SkipSubmissionCommit')) {
    $directory=Join-Path $output $mode
    New-Item -ItemType Directory -Path $directory | Out-Null
    & tar -xf $archive -C $directory
    if($LASTEXITCODE -ne 0){throw 'Cannot extract own archive'}
    $tests=Hashes $directory
    $original=(Get-FileHash -LiteralPath (Join-Path $directory $sourcePath) -Algorithm SHA256).Hash
    if($mode -ne 'Control') {
      & node (Join-Path $directory 'e2e/network/support/dual-verdict.mjs') --mutate (Join-Path $directory $sourcePath)
      if($LASTEXITCODE -ne 0){throw 'Exact single-production mutation failed'}
    }
    $entry=[ordered]@{name=$mode;tests=$tests;afterDualTests=$null;originalSource=$original;source=(Get-FileHash -LiteralPath (Join-Path $directory $sourcePath) -Algorithm SHA256).Hash;commands=@();baseline=$null;evidence=$null}
    $manifest.variants += $entry; Save-Manifest
    if(($tests|ConvertTo-Json -Compress) -cne ($manifest.variants[0].tests|ConvertTo-Json -Compress) -or $original -cne $manifest.variants[0].originalSource){throw 'Exact archive test/source identity required'}
    if(($entry.source -ceq $original) -ne ($mode -eq 'Control')){throw 'Only the mutant outbox may change'}
    Push-Location $directory
    try {
      $ciBefore=$env:CI;$env:CI='true'
      try{$code=Recorded ($mode+'-install') 'pnpm install --frozen-lockfile --filter auriga-dsh... --store-dir $AURIGA_PNPM_STORE' {& pnpm install --frozen-lockfile --filter 'auriga-dsh...' --store-dir $env:AURIGA_PNPM_STORE}}finally{$env:CI=$ciBefore}
      if($code -ne 0){throw 'Locked independent dependencies failed'}
      if($mode -eq 'Control') {
        $code=Recorded 'Control-guards' 'node --test e2e/network/dual-dsh.test.mjs e2e/network/dual-cleanup.test.mjs e2e/network/plugin-send.test.mjs e2e/network/host-install-cache.test.mjs e2e/network/native-install.test.mjs e2e/network/submit-client.test.mjs e2e/network/gateway-send-mailbox.test.mjs clients/dsh-plugin/test/login-routes.test.js' {& node --test e2e/network/dual-dsh.test.mjs e2e/network/dual-cleanup.test.mjs e2e/network/plugin-send.test.mjs e2e/network/host-install-cache.test.mjs e2e/network/native-install.test.mjs e2e/network/submit-client.test.mjs e2e/network/gateway-send-mailbox.test.mjs clients/dsh-plugin/test/login-routes.test.js}
        if($code -ne 0){throw 'Driver and existing SEND guards failed'}
      }
      Push-Location clients/dsh-plugin
      try{$code=Recorded ($mode+'-typecheck') 'node ../../node_modules/typescript/bin/tsc --noEmit -p tsconfig.json' {& node ../../node_modules/typescript/bin/tsc --noEmit -p tsconfig.json}}finally{Pop-Location}
      if($code -ne 0){throw 'Both production variants must typecheck'}
      $code=Recorded ($mode+'-java') "$MavenCommand -B -pl services/platform-app -am test -Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest -Dsurefire.failIfNoSpecifiedTests=false" {& $MavenCommand -B -pl services/platform-app -am test '-Dtest=NetworkSourceJavaHostTest,NetworkSourceJavaFaultsTest' '-Dsurefire.failIfNoSpecifiedTests=false'}
      if($code -ne 0){throw 'Fresh Java bridge compile/test failed'}
      $code=Recorded ($mode+'-entry') 'node e2e/network/entry-check.mjs --run' {& node e2e/network/entry-check.mjs --run}
      if($code -ne 0){throw 'Actual IAM entry headers must satisfy production RequestIdentity'}
      $code=Recorded ($mode+'-assembly') 'node e2e/network/support/dual-prepare.mjs --compile' {& node e2e/network/support/dual-prepare.mjs --compile}
      if($code -ne 0){throw 'Normal-login Java and real Team/network Go assembly must compile'}
      Push-Location infra/ai-gateway
      try{$code=Recorded ($mode+'-submit-mtls') 'go test -count=1 -run ^TestAssemblySubmitTLSUsesDedicatedClientCAAndRealRuntime$ -v ./cmd/network-source-assembly' {& go test -count=1 -run '^TestAssemblySubmitTLSUsesDedicatedClientCAAndRealRuntime$' -v ./cmd/network-source-assembly}}finally{Pop-Location}
      if($code -ne 0){throw 'Dedicated submit client CA must reach the real runtime and reject missing/foreign clients'}
      # These sources already belong to this exact archive. Do not recursively
      # git-archive from its untracked subdirectory (which is not a git tree).
      $edgeOverlay=Join-Path $output ($mode+'-edge-overlay.json')
      $edgeReplacements=@{}
      $edgeReplacements[(Join-Path $directory 'infra/edge/send4_platform_origin_test.go')]=Join-Path $directory 'e2e/network/support/send4-platform-origin_test.go'
      $edgeReplacements[(Join-Path $directory 'infra/edge/send4_edge_fixture_test.go')]=Join-Path $directory 'e2e/network/support/dual-edge_test.go'
      [IO.File]::WriteAllText($edgeOverlay,(@{Replace=$edgeReplacements}|ConvertTo-Json -Depth 5),$utf8)
      Push-Location infra/edge
      try{$code=Recorded ($mode+'-edge-routing') 'go test -overlay <edge-overlay.json> -count=1 -run ^(TestSEND4PlatformOrigin|TestSEND4EdgeFixtureLifecycle|TestDeviceNetworkSubmitRuntimeSeparation)$ -v .' {& go test -overlay $edgeOverlay -count=1 -run '^(TestSEND4PlatformOrigin|TestSEND4EdgeFixtureLifecycle|TestDeviceNetworkSubmitRuntimeSeparation)$' -v .}}finally{Pop-Location}
      if($code -ne 0){throw 'Separate HTTPS network submit edge routing must pass'}
      if($Run) {
        $runtimeBefore=$env:AURIGA_DSH_RUNTIME
        try {
          # Do not inherit a runtime from a different checkout during acceptance.
          $env:AURIGA_DSH_RUNTIME=$null
          $runExit=Recorded ($mode+'-run') 'node e2e/network/source-dual-dsh.mjs --run' {& node e2e/network/source-dual-dsh.mjs --run}
        } finally {$env:AURIGA_DSH_RUNTIME=$runtimeBefore}
        $evidence=@(Get-ChildItem -LiteralPath 'e2e/network/target' -Directory -Filter 'dual-dsh-*')
        if($evidence.Count -ne 1){throw 'One exact dual-DSH manifest required'}
        $entry.evidence=Join-Path $evidence[0].FullName 'manifest.json'
        & node e2e/network/support/dual-verdict.mjs --verdict $entry.evidence $mode $runExit
        if($LASTEXITCODE -ne 0){throw 'Dual DSH business verdict rejected; setup/SQL/cleanup never count'}
        # Cleanup must preserve the shared source tree before the baseline loads
        # any modules. Record exact post-cleanup hashes, including nats-binary.
        $entry.afterDualTests=Hashes $directory; Save-Manifest
        if(($entry.afterDualTests|ConvertTo-Json -Compress) -cne ($tests|ConvertTo-Json -Compress)){throw 'Source/test integrity changed during dual DSH run or cleanup'}
        # Keep the original assertions and its separate two-Context scope.
        if($mode -eq 'Control') {
          $baselineExit=Recorded 'Control-baseline' 'node e2e/network/source-plugin-send.mjs --run' {& node e2e/network/source-plugin-send.mjs --run}
          $baseline=@(Get-ChildItem -LiteralPath 'e2e/network/target' -Directory -Filter 'assembly-*')
          if($baseline.Count -ne 1){throw 'One original baseline manifest required'}
          $entry.baseline=Join-Path $baseline[0].FullName 'manifest.json'
          & node e2e/network/support/plugin-send-verdict.mjs --verdict $entry.baseline Control $baselineExit
          if($LASTEXITCODE -ne 0){throw 'Original T3b/T4/SEND-2/SEND-3 baseline failed; not counterevidence'}
        }
      }
      if(((Hashes $directory)|ConvertTo-Json -Compress) -cne ($tests|ConvertTo-Json -Compress)){throw 'Test sources changed'}
    } finally {Pop-Location; Save-Manifest}
  }
  $manifest.complete=$true
} finally {Save-Manifest;Write-Output "Dual DSH evidence: $output"}
if($CompileOnly){Write-Output 'CompileOnly: no PG or business acceptance/counterevidence claimed'}
