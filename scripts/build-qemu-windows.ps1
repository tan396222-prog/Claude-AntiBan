#requires -Version 7.0
[CmdletBinding()]
param(
    [string]$DependencyCache,
    [string]$OutputDirectory,
    [string]$MsysDirectory = $env:CAGE_MSYS2_ROOT
)
. (Join-Path $PSScriptRoot 'windows-common.ps1')
if (-not $IsWindows) { throw 'Build the Windows QEMU runtime in an MSYS2 UCRT64 environment on Windows.' }
$repository = Split-Path $PSScriptRoot -Parent
if (-not $DependencyCache) { $DependencyCache = Join-Path $repository '_output/windows-deps' }
if (-not $OutputDirectory) { $OutputDirectory = Join-Path $DependencyCache 'qemu-cage' }
$DependencyCache = [IO.Path]::GetFullPath($DependencyCache)
$OutputDirectory = [IO.Path]::GetFullPath($OutputDirectory)
$dependencies = Get-Content (Join-Path $repository 'packaging/windows/dependencies.json') -Raw | ConvertFrom-Json
$patchDirectory = Join-Path $repository 'patches/qemu'
$inputs = @((Join-Path $PSScriptRoot 'build-qemu-windows.sh'), $PSCommandPath) + @(Get-ChildItem $patchDirectory -Filter '*.patch' | Sort-Object Name | ForEach-Object FullName)
$fingerprintText = $dependencies.qemuSource.hash + $dependencies.qemuKeycodemap.hash + (($inputs | ForEach-Object { (Get-FileHash $_ -Algorithm SHA256).Hash }) -join '')
$fingerprint = [Convert]::ToHexString([Security.Cryptography.SHA256]::HashData([Text.Encoding]::UTF8.GetBytes($fingerprintText)))
$manifestPath = Join-Path $OutputDirectory 'cage-build.json'
if (Test-Path $manifestPath) {
    $manifest = Get-Content $manifestPath -Raw | ConvertFrom-Json
    if ($manifest.fingerprint -eq $fingerprint) {
        foreach ($file in $manifest.files) {
            if ((Get-FileHash (Join-Path $OutputDirectory $file.name) -Algorithm SHA256).Hash -ne $file.sha256) {
                throw 'The cached QEMU runtime differs from its build manifest. Use a new output directory.'
            }
        }
        Write-Host "Verified Windows QEMU runtime at $OutputDirectory"
        return
    }
}
if (-not $MsysDirectory) {
    $MsysDirectory = @('C:\msys64', 'D:\msys64') | Where-Object { Test-Path (Join-Path $_ 'ucrt64/bin/gcc.exe') } | Select-Object -First 1
}
if (-not $MsysDirectory -or -not (Test-Path (Join-Path $MsysDirectory 'ucrt64/bin/pkg-config.exe'))) {
    throw 'Set CAGE_MSYS2_ROOT to MSYS2 with the UCRT64 QEMU build dependencies described in docs/windows.md.'
}
$archive = Get-VerifiedGitArchive $dependencies.qemuSource $DependencyCache
$stage = Join-Path $repository ('_output/windows-qemu-' + [guid]::NewGuid().ToString('N'))
New-Item -ItemType Directory -Path $stage -Force | Out-Null
Invoke-BuildTool 'tar.exe' @('-xf', $archive, '-C', $stage)
$source = Join-Path $stage $dependencies.qemuSource.prefix.TrimEnd('/')
$keymapArchive = Get-VerifiedDependency $dependencies.qemuKeycodemap $DependencyCache
Invoke-BuildTool 'tar.exe' @('-xf', $keymapArchive, '-C', (Join-Path $source 'subprojects'))
Move-Item (Join-Path $source ('subprojects/keycodemapdb-' + $dependencies.qemuKeycodemap.version)) (Join-Path $source 'subprojects/keycodemapdb')
$savedSystem, $savedHere = $env:MSYSTEM, $env:CHERE_INVOKING
try {
    $env:MSYSTEM = 'UCRT64'
    $env:CHERE_INVOKING = '1'
    Invoke-BuildTool (Join-Path $MsysDirectory 'usr/bin/bash.exe') @('-l', (Join-Path $PSScriptRoot 'build-qemu-windows.sh'),
        $source, (Join-Path $stage 'build'), (Join-Path $stage 'runtime'), $patchDirectory)
    $built = Join-Path $stage 'runtime'
    $files = @(Get-ChildItem $built -File | ForEach-Object {
        @{ name = $_.Name; sha256 = (Get-FileHash $_.FullName -Algorithm SHA256).Hash }
    })
    @{ fingerprint = $fingerprint; source = $dependencies.qemuSource.commit; files = $files } |
        ConvertTo-Json -Depth 4 | Set-Content (Join-Path $built 'cage-build.json')
    New-Item -ItemType Directory -Path $OutputDirectory -Force | Out-Null
    Copy-Item (Join-Path $built '*') $OutputDirectory -Force
} finally {
    $env:MSYSTEM = $savedSystem
    $env:CHERE_INVOKING = $savedHere
}
Write-Host "Built Windows QEMU runtime at $OutputDirectory"
