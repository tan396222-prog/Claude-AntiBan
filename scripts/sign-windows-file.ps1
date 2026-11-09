#requires -Version 7.0
# Called by NSIS for the generated uninstaller; contains no credentials.
[CmdletBinding()]
param([Parameter(Mandatory)][string]$Path)
. (Join-Path $PSScriptRoot 'windows-common.ps1')
try { Sign-CageFile $Path (Get-SigningTool) }
catch { Write-Error $_; exit 1 }
