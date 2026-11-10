#requires -Version 5.1
param([string]$Implementation=(Join-Path $PSScriptRoot 'maintain-windows-workbench.ps1'))
$ErrorActionPreference='Stop'
$source=[IO.File]::ReadAllText($Implementation)
$parseErrors=$null
$ast=[Management.Automation.Language.Parser]::ParseInput($source,[ref]$null,[ref]$parseErrors)
if ($parseErrors.Count) { throw $parseErrors[0] }
$bindingStart=$source.IndexOf('$bindingPath=Join-Path')
if ($bindingStart -lt 0) { throw 'Maintenance preflight entry missing.' }
$prelude=$source.Substring(0,$bindingStart)
$elevation=$ast.Find({param($node)
    $node -is [Management.Automation.Language.IfStatementAst] -and $node.Clauses[0].Item1.Extent.Text.Contains('WindowsPrincipal')
},$true)
if (!$elevation) { throw 'Production elevation entry missing.' }
$elevationBody=$elevation.Clauses[0].Item2.Extent.Text
$elevationBody=$elevationBody.Substring(1,$elevationBody.Length-2).Replace('-ScriptPath $PSCommandPath','-ScriptPath $fixtureChild')
$hostOnly=$ast.Find({param($node)
    $node -is [Management.Automation.Language.IfStatementAst] -and $node.Clauses[0].Item1.Extent.Text -eq '$HostOnly'
},$true)
if (!$hostOnly -or !$hostOnly.Extent.Text.Contains('exit 0') -or $hostOnly.Extent.Text.Contains('New-PSSession') -or $hostOnly.Extent.StartOffset -gt $source.IndexOf('Get-Credential -Message')) { throw 'HostOnly must finish after host installation.' }

function Assert-Stage([string]$Command,[int]$Code) {
    $offset=$source.IndexOf($Command)
    if ($offset -lt 0) { throw "Stage receiver missing $Command" }
    $assignments=@($ast.FindAll({param($node)
        if ($node -isnot [Management.Automation.Language.AssignmentStatementAst] -or $node.Left.Extent.Text -ne '$maintenanceExitCode' -or $node.Right.Extent.Text -notmatch '^\d+$') { return $false }
        for ($parent=$node.Parent; $parent; $parent=$parent.Parent) {
            if ($parent -is [Management.Automation.Language.TrapStatementAst] -or $parent -is [Management.Automation.Language.FunctionDefinitionAst]) { return $false }
        }
        $true
    },$true) | Where-Object {$_.Extent.StartOffset -lt $offset} | Sort-Object {$_.Extent.StartOffset})
    if (!$assignments.Count -or [int]$assignments[-1].Right.Extent.Text -ne $Code) { throw "Wrong production failure stage for $Command" }
}
Assert-Stage 'Get-FileHash -LiteralPath $ManifestPath' 41
Assert-Stage "& (Join-Path `$kit 'install-windows-host-manager.ps1')" 42
Assert-Stage 'Get-PinnedPackage $spec.Value $cache' 46
Assert-Stage 'New-PSSession -VMId' 43
Assert-Stage "Invoke-Command -Session `$session -FilePath (Join-Path `$kit 'update-windows-workbench-components.ps1')" 44
Assert-Stage '[IO.File]::Replace' 45

$shell=(Get-Process -Id $PID).Path
$temporary=Join-Path ([IO.Path]::GetTempPath()) ('cage-maintenance-feedback-'+[guid]::NewGuid().ToString('N'))
$null=New-Item -ItemType Directory -Path $temporary
$encoding=[Text.UTF8Encoding]::new($true)
function Quote-Literal([string]$Value) { "'"+$Value.Replace("'","''")+"'" }
function Run-Script([string]$Path,[hashtable]$Parameters) {
    $payload=[Convert]::ToBase64String([Text.Encoding]::UTF8.GetBytes(($Parameters|ConvertTo-Json -Compress)))
    $command='$p=[Text.Encoding]::UTF8.GetString([Convert]::FromBase64String('''+$payload+'''))|ConvertFrom-Json;$a=@{};$p.PSObject.Properties|ForEach-Object{$a[$_.Name]=$_.Value};& '+(Quote-Literal $Path)+' @a;exit $LASTEXITCODE'
    $encoded=[Convert]::ToBase64String([Text.Encoding]::Unicode.GetBytes($command))
    $start=[Diagnostics.ProcessStartInfo]::new()
    $start.FileName=$shell
    $start.Arguments='-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -EncodedCommand '+$encoded
    $start.UseShellExecute=$false
    $start.RedirectStandardOutput=$true
    $start.RedirectStandardError=$true
    $process=[Diagnostics.Process]::new()
    $process.StartInfo=$start
    try {
        if (!$process.Start()) { throw 'Fixture process did not start.' }
        $stdout=$process.StandardOutput.ReadToEndAsync()
        $stderr=$process.StandardError.ReadToEndAsync()
        if (!$process.WaitForExit(15000)) { $process.Kill(); throw 'Fixture process timed out.' }
        [pscustomobject]@{Code=$process.ExitCode;Output=$stdout.GetAwaiter().GetResult();Error=$stderr.GetAwaiter().GetResult()}
    } finally { $process.Dispose() }
}

try {
    $profile=Join-Path $temporary "profile's 中文"
    $null=New-Item -ItemType Directory -Path $profile
    $binding=Join-Path $profile 'windows-guest.json'
    $manifest=Join-Path $temporary 'manifest.json'
    [IO.File]::WriteAllText($binding,'{}',$encoding)
    [IO.File]::WriteAllText($manifest,'{}',$encoding)
    $parameters=@{Profile=$profile;ManifestPath=$manifest;ManifestSHA256=(Get-FileHash $manifest).Hash}
    foreach ($name in @('binding','manifest')) {
        $requestParameters=$parameters.Clone()
        if ($name -eq 'binding') { $requestParameters.BindingSHA256='a'*64 } else { $requestParameters.ManifestSHA256='b'*64 }
        $result=Run-Script $Implementation $requestParameters
        if ($result.Code -ne 41 -or !$result.Error.Contains('CAGE_WINDOWS_MAINTENANCE_VALIDATION')) { throw "Real $name preflight lost its phase $($result | ConvertTo-Json -Compress)" }
    }

    # The production RunAs block launches real isolated PowerShell children.
    # Only the OS elevation call is replaced, so these tests require no UAC,
    # VM, administrator credential or protected Windows directory.
    $mock=@'
function Join-Path($Path,$ChildPath) {
    if ($ChildPath -eq 'WindowsPowerShell/v1.0/powershell.exe') { return $fixtureShell }
    Microsoft.PowerShell.Management\Join-Path -Path $Path -ChildPath $ChildPath
}
function Start-Process($FilePath,$ArgumentList,$Verb,[switch]$PassThru) {
    if ($FilePath -ne $fixtureShell -or $Verb -ne 'RunAs' -or !$PassThru) { throw 'Incorrect elevation contract.' }
    if ($fixtureCancel) { throw [InvalidOperationException]::new('synthetic nested launch error',[ComponentModel.Win32Exception]::new(1223)) }
    if ($fixtureLaunchFailure) { throw [ComponentModel.Win32Exception]::new(5) }
    Microsoft.PowerShell.Management\Start-Process -FilePath $FilePath -ArgumentList $ArgumentList -PassThru -NoNewWindow
}
'@
    $cases=@(
        @{Name='regular-success';Code=0;Phase='';HostOnly=$false;Tools=$true},
        @{Name='host-success';Code=0;Phase='';HostOnly=$true;Tools=$false},
        @{Name='validation';Code=41;Phase='VALIDATION';HostOnly=$true},
        @{Name='manager';Code=42;Phase='HOST_MANAGER';HostOnly=$true},
        @{Name='guest-connection';Code=43;Phase='GUEST_CONNECTION';HostOnly=$false},
        @{Name='guest-components';Code=44;Phase='GUEST_COMPONENTS';HostOnly=$false},
        @{Name='host-relay';Code=45;Phase='HOST_RELAY';HostOnly=$false},
        @{Name='prepare-tools';Code=46;Phase='PACKAGE_PREPARATION';HostOnly=$false;Tools=$true},
        @{Name='launch-failed';Code=47;Phase='AUTHORIZATION';HostOnly=$true;LaunchFailure=$true},
        @{Name='uac-canceled';Code=48;Phase='AUTHORIZATION_CANCELED';HostOnly=$true;Cancel=$true},
        @{Name='legacy-failed';Code=1;Phase='FAILED';HostOnly=$true;Legacy=$true},
        @{Name='unknown-exit';Code=1;Phase='FAILED';HostOnly=$false;Unknown=$true}
    )
    foreach ($case in $cases) {
        $child=Join-Path $temporary ($case.Name+"-child's 中文.ps1")
        $expectedHost=([bool]$case.HostOnly).ToString().ToLowerInvariant()
        $expectedTools=([bool]$case.Tools).ToString().ToLowerInvariant()
        $childBody="`nif ([bool]`$HostOnly -ne `$$expectedHost -or [bool]`$Tools -ne `$$expectedTools -or `$GuestAdministrator -or `$BindingSHA256 -ne ('c'*64)) { exit 92 }`n"
        if ($case.Unknown) { $childBody+='exit 77' }
        elseif ($case.Legacy) { $childBody+='throw ''synthetic legacy error''' }
        elseif ($case.Code -eq 0) {
            $childBody+=$hostOnly.Extent.Text+"`nWrite-Output 'synthetic regular success'`nexit 0"
        } else { $childBody+="`$maintenanceExitCode=$($case.Code)`nthrow 'synthetic stage error'" }
        if ($case.Legacy) {
            [IO.File]::WriteAllText($child,$ast.ParamBlock.Extent.Text+$childBody,$encoding)
        } else { [IO.File]::WriteAllText($child,$prelude+$childBody,$encoding) }
        $parentScript=Join-Path $temporary ($case.Name+'-parent.ps1')
        $config="`n`$fixtureShell="+(Quote-Literal $shell)+"`n`$fixtureChild="+(Quote-Literal $child)+"`n`$fixtureCancel=`$"+([bool]$case.Cancel).ToString().ToLowerInvariant()+"`n`$fixtureLaunchFailure=`$"+([bool]$case.LaunchFailure).ToString().ToLowerInvariant()+"`n`$bindingHash='c'*64`n`$maintenanceExitCode=47`n"
        $config+="`$GuestAdministrator=[pscredential]::new('synthetic-maintenance-user',(ConvertTo-SecureString 'synthetic-credential' -AsPlainText -Force))`n"
        [IO.File]::WriteAllText($parentScript,$prelude+$config+$mock+"`n"+$elevationBody,$encoding)
        $requestParameters=$parameters.Clone()
        $requestParameters.HostOnly=[bool]$case.HostOnly
        $requestParameters.Tools=[bool]$case.Tools
        $result=Run-Script $parentScript $requestParameters
        if ($result.Code -ne $case.Code -or ($case.Code -ne 0 -and !$result.Error.Contains('CAGE_WINDOWS_MAINTENANCE_'+$case.Phase))) {
            throw "RunAs stage lost for $($case.Name) $($result | ConvertTo-Json -Compress)"
        }
        Write-Output ("Passed {0} with exit {1}" -f $case.Name,$result.Code)
    }
    Write-Output 'Passed real binding/manifest preflight, production stage ownership, HostOnly, regular Tools, explicit child exits, legacy errors and nested UAC cancellation (isolated processes, simulated elevation).'
} finally {
    Remove-Item -LiteralPath $temporary -Recurse -Force
}
