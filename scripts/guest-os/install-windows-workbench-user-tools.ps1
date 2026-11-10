# Runs once as the new, non-elevated Cage workbench user. The administrator
# already provisioned the verified MSIX machine-wide; finishing registration
# here makes Windows consider it usable in this particular user session.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$BundleDirectory,[Parameter(Mandatory)][string]$BridgeDirectory)
$ErrorActionPreference='Stop'
$user=[Security.Principal.WindowsIdentity]::GetCurrent()
if ([Security.Principal.WindowsPrincipal]::new($user).IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) { throw 'Run as the dedicated standard guest user.' }
Add-AppxPackage -RegisterByFamilyName -MainPackage 'Claude_pzs8sxrjxfjjc' -ErrorAction Stop
& (Join-Path $PSScriptRoot 'install-cli.ps1') -BundleDirectory $BundleDirectory -BridgeDirectory $BridgeDirectory -WorkbenchSetup
