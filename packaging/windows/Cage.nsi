Unicode true
RequestExecutionLevel user
; Per-file decompression avoids a full payload copy in the system TEMP drive.
SetCompressor lzma
SetCompressorDictSize 32
ManifestDPIAware true
ManifestSupportedOS all

!include MUI2.nsh
!define MUI_ICON "Cage.ico"
!define MUI_UNICON "Cage.ico"
!include LogicLib.nsh
!include FileFunc.nsh
!include x64.nsh
!include WinVer.nsh
!include nsDialogs.nsh

!ifndef APP_DIR
  !error "APP_DIR is required"
!endif
!ifndef VERSION
  !error "VERSION is required"
!endif
!ifndef VERSION_NUMERIC
  !error "VERSION_NUMERIC is required"
!endif
!ifndef OUTPUT_FILE
  !error "OUTPUT_FILE is required"
!endif

!define APP_ID "io.github.lee-xeo.claude-cage"
!define UNINSTALL_KEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\ClaudeCage"
!define WEBVIEW2_APP_ID "{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}"
Name "Cage ${VERSION}"
OutFile "${OUTPUT_FILE}"
InstallDir "$LOCALAPPDATA\Programs\Cage"
InstallDirRegKey HKCU "${UNINSTALL_KEY}" "InstallLocation"
VIProductVersion "${VERSION_NUMERIC}"
VIAddVersionKey /LANG=1033 "ProductName" "Cage"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "FileDescription" "Cage Windows Installer"
VIAddVersionKey /LANG=1033 "LegalCopyright" "Cage contributors"

!ifdef SIGN_SCRIPT
  !uninstfinalize '"${SIGN_PWSH}" -NoProfile -File "${SIGN_SCRIPT}" -Path "%1"' = 0
!endif

!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN "$INSTDIR\Cage.exe"
!define MUI_FINISHPAGE_RUN_NOTCHECKED
!define MUI_FINISHPAGE_TEXT "Cage is installed.$\r$\n$\r$\nIf virtualization is unavailable, enable Windows Hypervisor Platform in Windows Features and restart Windows. Your projects and Cage data are preserved during upgrades and uninstall."
!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_LICENSE "${APP_DIR}/LICENSE.txt"
!define MUI_DIRECTORYPAGE_TEXT_TOP "$(CageDirectoryHelp)"
!insertmacro MUI_PAGE_DIRECTORY
Page custom ShortcutOptionsPage ShortcutOptionsLeave
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

LangString CageDirectoryHelp ${LANG_ENGLISH} "Choose where to install the Cage application. On first launch, you can choose a separate drive for the Linux image, download cache, and virtual disks before starting the environment."
LangString CageDirectoryHelp ${LANG_SIMPCHINESE} "请选择 Cage 程序的安装目录。首次打开 Cage 后，可以在启动环境前单独选择存放 Linux 镜像、下载缓存和虚拟磁盘的磁盘。"
LangString CageShortcutsTitle ${LANG_ENGLISH} "Shortcuts"
LangString CageShortcutsTitle ${LANG_SIMPCHINESE} "快捷方式"
LangString CageShortcutsHelp ${LANG_ENGLISH} "Choose where to add Cage shortcuts for your Windows account."
LangString CageShortcutsHelp ${LANG_SIMPCHINESE} "选择为当前 Windows 用户添加的 Cage 快捷方式。"
LangString CageDesktopShortcut ${LANG_ENGLISH} "Add a desktop shortcut"
LangString CageDesktopShortcut ${LANG_SIMPCHINESE} "添加桌面快捷方式"
LangString CageStartMenuShortcuts ${LANG_ENGLISH} "Add to the Start menu"
LangString CageStartMenuShortcuts ${LANG_SIMPCHINESE} "添加到开始菜单"

Var UpdateMode
Var StageDir
Var BackupDir
Var ExistingInstall
Var DesktopShortcut
Var StartMenuShortcuts
Var DesktopShortcutControl
Var StartMenuShortcutsControl
Var InstallationMutex
Var TransactionRecord
Var DiagnosticFile
Var InstallPhase
Var InstallOutcome
Var HelperExit
Var DiagnosticArguments

Function WriteInstallDiagnostic
  ${If} $DiagnosticFile != ""
    Push $2
    Push $3
    StrCpy $3 0
    IfErrors 0 +2
      StrCpy $3 1
    ClearErrors
    FileOpen $2 "$DiagnosticFile" w
    ${IfNot} ${Errors}
      FileWrite $2 '{"phase":"$InstallPhase","status":"$InstallOutcome","helperExit":"$HelperExit"}$\r$\n'
      FileClose $2
    ${EndIf}
    ${If} $3 == 1
      SetErrors
    ${Else}
      ClearErrors
    ${EndIf}
    Pop $3
    Pop $2
  ${EndIf}
FunctionEnd

!macro InstallPhase NAME
  StrCpy $InstallPhase "${NAME}"
  Call WriteInstallDiagnostic
!macroend

Function un.WriteInstallDiagnostic
  ${If} $DiagnosticFile != ""
    Push $2
    Push $3
    StrCpy $3 0
    IfErrors 0 +2
      StrCpy $3 1
    ClearErrors
    FileOpen $2 "$DiagnosticFile" w
    ${IfNot} ${Errors}
      FileWrite $2 '{"phase":"$InstallPhase","status":"$InstallOutcome","helperExit":"$HelperExit"}$\r$\n'
      FileClose $2
    ${EndIf}
    ${If} $3 == 1
      SetErrors
    ${Else}
      ClearErrors
    ${EndIf}
    Pop $3
    Pop $2
  ${EndIf}
FunctionEnd

!macro UninstallPhase NAME
  StrCpy $InstallPhase "${NAME}"
  Call un.WriteInstallDiagnostic
!macroend

Function un.onUninstFailed
  StrCpy $InstallOutcome "failed"
  Call un.WriteInstallDiagnostic
FunctionEnd

Function un.onUninstSuccess
  StrCpy $InstallOutcome "success"
  !insertmacro UninstallPhase "completed"
FunctionEnd

Function .onInstFailed
  StrCpy $InstallOutcome "failed"
  Call WriteInstallDiagnostic
FunctionEnd

Function .onInstSuccess
  StrCpy $InstallOutcome "success"
  Call WriteInstallDiagnostic
FunctionEnd

!macro AcquireInstallationMutex
  ; NSIS and UpdateInstaller both reject an existing named object and keep
  ; their handle until the transaction ends. No caller waits for ownership.
  System::Call 'kernel32::CreateMutexW(p 0, i 0, w "Global\Cage.Installation") p.r0 ?e'
  Pop $1
  ${If} $0 == 0
    SetErrorLevel 2
    Abort "无法锁定 Cage 安装操作，请核对系统权限后重试。"
  ${EndIf}
  StrCpy $InstallationMutex $0
  ${If} $1 == 183
    SetErrorLevel 2
    Abort "另一个 Cage 安装或卸载仍在进行，请等待完成后重试。"
  ${EndIf}
!macroend

Function .onGUIEnd
  ${If} $InstallationMutex != ""
    System::Call 'kernel32::CloseHandle(p $InstallationMutex)'
  ${EndIf}
FunctionEnd

Function un.onGUIEnd
  ${If} $InstallationMutex != ""
    System::Call 'kernel32::CloseHandle(p $InstallationMutex)'
  ${EndIf}
FunctionEnd

Function LoadShortcutOptions
  ; Fresh installs enable both entries. Older installs inherit actual shortcuts.
  StrCpy $DesktopShortcut 1
  StrCpy $StartMenuShortcuts 1
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $DesktopShortcut 0
    StrCpy $StartMenuShortcuts 0
    ${If} ${FileExists} "$DESKTOP\Cage.lnk"
      StrCpy $DesktopShortcut 1
    ${EndIf}
    ${If} ${FileExists} "$SMPROGRAMS\Cage\Cage.lnk"
      StrCpy $StartMenuShortcuts 1
    ${EndIf}
  ${EndIf}
  ClearErrors
  ReadRegDWORD $0 HKCU "${UNINSTALL_KEY}" "DesktopShortcut"
  ${IfNot} ${Errors}
    StrCpy $DesktopShortcut $0
  ${EndIf}
  ClearErrors
  ReadRegDWORD $0 HKCU "${UNINSTALL_KEY}" "StartMenuShortcuts"
  ${IfNot} ${Errors}
    StrCpy $StartMenuShortcuts $0
  ${EndIf}
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/DESKTOPSHORTCUT=" $1
  ${IfNot} ${Errors}
    ${If} $1 != "0"
    ${AndIf} $1 != "1"
      SetErrorLevel 2
      Abort "DESKTOPSHORTCUT must be 0 or 1."
    ${EndIf}
    StrCpy $DesktopShortcut $1
  ${EndIf}
  ClearErrors
  ${GetOptions} $0 "/STARTMENUSHORTCUT=" $1
  ${IfNot} ${Errors}
    ${If} $1 != "0"
    ${AndIf} $1 != "1"
      SetErrorLevel 2
      Abort "STARTMENUSHORTCUT must be 0 or 1."
    ${EndIf}
    StrCpy $StartMenuShortcuts $1
  ${EndIf}
FunctionEnd

Function ShortcutOptionsPage
  !insertmacro MUI_HEADER_TEXT "$(CageShortcutsTitle)" "$(CageShortcutsHelp)"
  nsDialogs::Create 1018
  Pop $0
  ${If} $0 == error
    Abort
  ${EndIf}
  ${NSD_CreateCheckbox} 0 8u 100% 16u "$(CageDesktopShortcut)"
  Pop $DesktopShortcutControl
  ${NSD_SetState} $DesktopShortcutControl $DesktopShortcut
  ${NSD_CreateCheckbox} 0 36u 100% 16u "$(CageStartMenuShortcuts)"
  Pop $StartMenuShortcutsControl
  ${NSD_SetState} $StartMenuShortcutsControl $StartMenuShortcuts
  ${NSD_OnBack} ShortcutOptionsLeave
  nsDialogs::Show
FunctionEnd

Function ShortcutOptionsLeave
  ${NSD_GetState} $DesktopShortcutControl $DesktopShortcut
  ${NSD_GetState} $StartMenuShortcutsControl $StartMenuShortcuts
FunctionEnd

Function SetDefaultInstallDirectory
  ; NSIS removes /D= from $CMDLINE, so inspect the original command line.
  System::Call 'kernel32::GetCommandLineW() w .r0'
  ClearErrors
  ${GetOptions} $0 "/D=" $1
  ${IfNot} ${Errors}
    Return
  ${EndIf}
  ; Keep upgrades in the registered location, including installations on C:.
  ReadRegStr $0 HKCU "${UNINSTALL_KEY}" "InstallLocation"
  ${If} $0 != ""
    StrCpy $INSTDIR $0
    Return
  ${EndIf}
  System::Call 'kernel32::GetDriveTypeW(w "D:\") i .r0'
  ${If} $0 == 3
    StrCpy $INSTDIR "D:\Cage"
  ${EndIf}
FunctionEnd

!macro CheckStopped FILE
  ${If} ${FileExists} "$INSTDIR\${FILE}"
    ; Exclusive write access also detects images whose VM/controller has no window.
    System::Call 'kernel32::CreateFileW(w "$INSTDIR\${FILE}", i 0x40000000, i 0, p 0, i 3, i 0, p 0) p .r0'
    ${If} $0 == -1
      StrCpy $HelperExit "application_file_in_use"
      SetErrorLevel 2
      Abort "Quit Cage and stop its environment before installing or uninstalling."
    ${EndIf}
    System::Call 'kernel32::CloseHandle(p r0)'
  ${EndIf}
!macroend

Function .onInit
  StrCpy $InstallOutcome "running"
  StrCpy $HelperExit ""
  ${GetParameters} $0
  ${GetOptions} $0 "/DIAGNOSTICS=" $DiagnosticFile
  StrCpy $DiagnosticArguments ""
  ${If} $DiagnosticFile != ""
    StrCpy $DiagnosticArguments '-Diagnostics "$DiagnosticFile.helper.json"'
  ${EndIf}
  !insertmacro InstallPhase "initializing"
  SetShellVarContext current
  SetRegView 64
  ${IfNot} ${IsNativeAMD64}
    MessageBox MB_ICONSTOP "This Cage package requires an Intel/AMD 64-bit version of Windows. Windows on ARM and 32-bit Windows are not supported."
    SetErrorLevel 1633
    Quit
  ${EndIf}
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\Windows NT\CurrentVersion" "CurrentBuildNumber"
  ${If} $0 < 19045
    MessageBox MB_ICONSTOP "Cage requires Windows 10 22H2 (build 19045) or Windows 11. Update Windows and try again."
    SetErrorLevel 1633
    Quit
  ${EndIf}
  Call SetDefaultInstallDirectory
  Call LoadShortcutOptions
  StrCpy $UpdateMode "0"
  ${GetParameters} $0
  ClearErrors
  ${GetOptions} $0 "/UPDATE" $1
  ${IfNot} ${Errors}
    StrCpy $UpdateMode "1"
    SetSilent silent
  ${EndIf}
FunctionEnd

Function CheckDestination
  ${GetRoot} "$INSTDIR" $0
  ${If} $0 == ""
  ${OrIf} $INSTDIR == $0
  ${OrIf} $INSTDIR == "$0\"
    SetErrorLevel 2
    Abort "Choose a dedicated application folder."
  ${EndIf}
  StrCpy $ExistingInstall "0"
  IfFileExists "$INSTDIR\Cage.install-id" existing
  ; IfFileExists with a wildcard also matches an empty directory.
  ${DirState} "$INSTDIR" $0
  StrCmp $0 1 foreign done
  existing:
    FileOpen $0 "$INSTDIR\Cage.install-id" r
    FileRead $0 $1
    FileClose $0
    ${If} $1 != "${APP_ID}"
      Goto foreign
    ${EndIf}
    StrCpy $ExistingInstall "1"
    ; Never stop processes by their name or force-close user workloads.
    !insertmacro CheckStopped "Cage.exe"
    !insertmacro CheckStopped "resources\cage.exe"
    !insertmacro CheckStopped "resources\mihomo.exe"
    !insertmacro CheckStopped "resources\runtime\qemu\qemu-system-x86_64.exe"
    Goto done
  foreign:
    SetErrorLevel 2
    Abort "The destination contains files from another installation. Choose an empty folder."
  done:
    ClearErrors
FunctionEnd

Function RestorePreservedFiles
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation restore -Target "$BackupDir" -Source "$StageDir" -Receipt "$TransactionRecord" $DiagnosticArguments'
  Pop $0
  Pop $1
  ${If} $0 != 0
    DetailPrint "$1"
    SetErrorLevel 2
    Abort "安装恢复尚未完成。文件分别保留在 $BackupDir 和 $StageDir，请保留这两个目录后重试。"
  ${EndIf}
  ClearErrors
  Rename "$BackupDir" "$INSTDIR"
  ${If} ${Errors}
    SetErrorLevel 2
    Abort "原安装目录未能恢复。文件保留在 $BackupDir，新程序保留在 $StageDir。"
  ${EndIf}
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation complete -Target "$INSTDIR" -Receipt "$TransactionRecord" $DiagnosticArguments'
  Pop $0
  Pop $1
  ${If} $0 != 0
    SetErrorLevel 2
    Abort "仍有文件需要核对，安装回执保留在 $TransactionRecord。请保留相关目录后处理。"
  ${EndIf}
FunctionEnd

Section "Cage" Main
  !insertmacro InstallPhase "acquiring_installation_lock"
  !insertmacro AcquireInstallationMutex
  StrCpy $TransactionRecord "$INSTDIR.cage-installation.json"
  ${If} ${FileExists} "$TransactionRecord"
    SetErrorLevel 2
    Abort "存在未处理的安装事务记录：$TransactionRecord。请先按记录恢复保留的目录，再重试。"
  ${EndIf}
  !insertmacro InstallPhase "checking_destination"
  Call CheckDestination
  ${GetParent} "$INSTDIR" $0
  CreateDirectory "$0"
  GetTempFileName $StageDir "$0"
  Delete "$StageDir"
  CreateDirectory "$StageDir"
  !insertmacro InstallPhase "unpacking_application"
  SetOutPath "$StageDir"
  ClearErrors
!ifdef NSIS_WIN32_MAKENSIS
  File /r "${APP_DIR}\*"
!else
  File /r "${APP_DIR}/*"
!endif
  WriteUninstaller "$StageDir\Uninstall.exe"
  ${If} ${Errors}
    SetOutPath "$TEMP"
    RMDir /r "$StageDir"
    SetErrorLevel 2
    Abort "Could not unpack Cage. Check free disk space and folder permissions."
  ${EndIf}

  ; Install the bundled offline runtime only when WebView2 is absent.
  !insertmacro InstallPhase "detecting_webview_runtime"
  SetRegView 32
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_APP_ID}" "pv"
  ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_APP_ID}" "pv"
  SetRegView 64
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ${If} $1 == ""
    ${OrIf} $1 == "0.0.0.0"
      DetailPrint "Installing the bundled Microsoft WebView2 runtime..."
      !insertmacro InstallPhase "installing_webview_runtime"
      ExecWait '"$StageDir\resources\runtime\MicrosoftEdgeWebView2RuntimeInstallerX64.exe" /silent /install' $0
      StrCpy $HelperExit $0
      Call WriteInstallDiagnostic
      ${If} $0 != 0
        SetOutPath "$TEMP"
        RMDir /r "$StageDir"
        SetErrorLevel 2
        Abort "Microsoft WebView2 installation failed ($0). Install the runtime and retry."
      ${EndIf}
    ${EndIf}
  ${EndIf}

  SetOutPath "$TEMP"
  StrCpy $BackupDir ""
  ${If} $ExistingInstall == "1"
    ${GetParent} "$INSTDIR" $0
    GetTempFileName $BackupDir "$0"
    Delete "$BackupDir"
  ${EndIf}
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=installation-files.ps1 "installation-files.ps1"
  File /oname=verified-file-deletion.cs "verified-file-deletion.cs"
  DetailPrint "正在检查并保留已有项目和修改文件…"
  !insertmacro InstallPhase "preparing_file_transaction"
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation prepare -Target "$StageDir" -Source "$INSTDIR" -Backup "$BackupDir" -Receipt "$TransactionRecord" $DiagnosticArguments'
  Pop $0
  Pop $1
  StrCpy $HelperExit $0
  Call WriteInstallDiagnostic
  DetailPrint "$1"
  ${If} $0 != 0
    SetOutPath "$TEMP"
    RMDir /r "$StageDir"
    SetErrorLevel 2
    Abort "无法安全保留现有文件，安装已停止。原安装目录已保留，请检查磁盘空间、文件占用和安装日志。"
  ${EndIf}
  SetOutPath "$TEMP"
  ${If} $ExistingInstall == "1"
    !insertmacro InstallPhase "moving_previous_application"
    ClearErrors
    Rename "$INSTDIR" "$BackupDir"
    ${If} ${Errors}
      nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation complete -Target "$INSTDIR" -Receipt "$TransactionRecord" $DiagnosticArguments'
      Pop $0
      Pop $1
      RMDir /r "$StageDir"
      SetErrorLevel 2
      Abort "Could not move the installed application. Quit Cage and retry."
    ${EndIf}
    DetailPrint "正在迁入个人文件并保留原有链接…"
    !insertmacro InstallPhase "moving_personal_files"
    nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation move -Target "$StageDir" -Source "$BackupDir" -Receipt "$TransactionRecord" $DiagnosticArguments'
    Pop $0
    Pop $1
    StrCpy $HelperExit $0
    Call WriteInstallDiagnostic
    DetailPrint "$1"
    ${If} $0 != 0
      Call RestorePreservedFiles
      RMDir /r "$StageDir"
      SetErrorLevel 2
      Abort "个人文件未能迁入新程序目录，原安装已恢复。请关闭占用文件的程序后重试。"
    ${EndIf}
  ${Else}
    RMDir "$INSTDIR"
  ${EndIf}
  !insertmacro InstallPhase "publishing_application"
  ClearErrors
  Rename "$StageDir" "$INSTDIR"
  ${If} ${Errors}
    ${If} $BackupDir != ""
      Call RestorePreservedFiles
    ${EndIf}
    RMDir /r "$StageDir"
    SetErrorLevel 2
    Abort "Could not install Cage. The previous installation has been restored."
  ${EndIf}
  ${If} $BackupDir != ""
    !insertmacro InstallPhase "cleaning_previous_application"
    DetailPrint "正在清理已核对的旧程序文件…"
    nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation cleanup -Target "$INSTDIR" -Source "$BackupDir" -Receipt "$TransactionRecord" $DiagnosticArguments'
    Pop $0
    Pop $1
    StrCpy $HelperExit $0
    Call WriteInstallDiagnostic
    DetailPrint "$1"
    ${If} $0 != 0
      DetailPrint "旧目录中仍有需要保留的文件：$BackupDir"
      SetErrorLevel 2
      Abort "安装仍有待恢复的文件。请保留 $TransactionRecord 及其中记录的目录，再处理恢复。"
    ${EndIf}
  ${Else}
    !insertmacro InstallPhase "completing_file_transaction"
    nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation complete -Target "$INSTDIR" -Receipt "$TransactionRecord" $DiagnosticArguments'
    Pop $0
    Pop $1
    StrCpy $HelperExit $0
    Call WriteInstallDiagnostic
    ${If} $0 != 0
      SetErrorLevel 2
      Abort "安装回执未能完成，记录保留在 $TransactionRecord。"
    ${EndIf}
  ${EndIf}
  !insertmacro InstallPhase "registering_installation"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Cage"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "Cage contributors"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\Cage.exe,0"
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  SetOutPath "$INSTDIR"
  ${If} $DesktopShortcut == 1
    CreateShortcut "$DESKTOP\Cage.lnk" "$INSTDIR\Cage.exe"
  ${Else}
    Delete "$DESKTOP\Cage.lnk"
  ${EndIf}
  ${If} $StartMenuShortcuts == 1
    CreateDirectory "$SMPROGRAMS\Cage"
    CreateShortcut "$SMPROGRAMS\Cage\Cage.lnk" "$INSTDIR\Cage.exe"
    CreateShortcut "$SMPROGRAMS\Cage\Uninstall Cage.lnk" "$INSTDIR\Uninstall.exe"
  ${Else}
    Delete "$SMPROGRAMS\Cage\Cage.lnk"
    Delete "$SMPROGRAMS\Cage\Uninstall Cage.lnk"
    RMDir "$SMPROGRAMS\Cage"
  ${EndIf}
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "DesktopShortcut" $DesktopShortcut
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "StartMenuShortcuts" $StartMenuShortcuts
  !insertmacro InstallPhase "completed"
  SetErrorLevel 0
SectionEnd

Function un.onInit
  StrCpy $InstallOutcome "running"
  StrCpy $HelperExit ""
  StrCpy $DiagnosticArguments ""
  ${GetParameters} $0
  ${GetOptions} $0 "/DIAGNOSTICS=" $DiagnosticFile
  ${If} $DiagnosticFile != ""
    StrCpy $DiagnosticArguments '-Diagnostics "$DiagnosticFile.helper.json"'
  ${EndIf}
  !insertmacro UninstallPhase "checking_installation_identity"
  SetShellVarContext current
  SetRegView 64
  FileOpen $0 "$INSTDIR\Cage.install-id" r
  FileRead $0 $1
  FileClose $0
  ${If} $1 != "${APP_ID}"
    StrCpy $HelperExit "identity_mismatch"
    Call un.WriteInstallDiagnostic
    SetErrorLevel 2
    Abort "This is not a Cage installation folder."
  ${EndIf}
  !insertmacro UninstallPhase "checking_stopped_processes"
  !insertmacro CheckStopped "Cage.exe"
  !insertmacro CheckStopped "resources\cage.exe"
  !insertmacro CheckStopped "resources\mihomo.exe"
  !insertmacro CheckStopped "resources\runtime\qemu\qemu-system-x86_64.exe"
FunctionEnd

Section "Uninstall"
  !insertmacro UninstallPhase "acquiring_installation_lock"
  !insertmacro AcquireInstallationMutex
  !insertmacro UninstallPhase "checking_pending_transaction"
  ${If} ${FileExists} "$INSTDIR.cage-installation.json"
    StrCpy $HelperExit "pending_install_transaction"
    Call un.WriteInstallDiagnostic
    SetErrorLevel 2
    Abort "存在待恢复的安装事务，请先处理 $INSTDIR.cage-installation.json 中记录的目录。"
  ${EndIf}
  InitPluginsDir
  SetOutPath "$PLUGINSDIR"
  File /oname=installation-files.ps1 "installation-files.ps1"
  File /oname=verified-file-deletion.cs "verified-file-deletion.cs"
  File /oname=install-files.json "${FILE_MANIFEST}"
  ; PowerShell 5.1 Add-Type must not resolve NSIS's native System.dll as a
  ; managed compiler reference from the plugin working directory.
  SetOutPath "$TEMP"
  !insertmacro UninstallPhase "removing_owned_files"
  nsExec::ExecToStack '"$SYSDIR\WindowsPowerShell\v1.0\powershell.exe" -NoProfile -ExecutionPolicy Bypass -File "$PLUGINSDIR\installation-files.ps1" -Operation uninstall -Target "$INSTDIR" -Manifest "$PLUGINSDIR\install-files.json" $DiagnosticArguments'
  Pop $0
  Pop $1
  StrCpy $HelperExit $0
  Call un.WriteInstallDiagnostic
  DetailPrint "$1"
  ${If} $0 != 0
    SetErrorLevel 2
    Abort "部分程序文件正在使用或无法读取，卸载未完成。个人文件已保留，请关闭占用程序后重试。"
  ${EndIf}
  !insertmacro UninstallPhase "removing_shortcuts_and_registration"
  Delete "$DESKTOP\Cage.lnk"
  Delete "$SMPROGRAMS\Cage\Cage.lnk"
  Delete "$SMPROGRAMS\Cage\Uninstall Cage.lnk"
  RMDir "$SMPROGRAMS\Cage"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  !insertmacro UninstallPhase "removing_install_directory"
  RMDir "$INSTDIR"
SectionEnd
