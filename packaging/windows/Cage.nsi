Unicode true
RequestExecutionLevel user
SetCompressor /SOLID lzma
SetCompressorDictSize 32
ManifestDPIAware true
ManifestSupportedOS all
Icon "Cage.ico"
UninstallIcon "Cage.ico"

!include MUI2.nsh
!include LogicLib.nsh
!include FileFunc.nsh
!include x64.nsh
!include WinVer.nsh

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
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES
!insertmacro MUI_LANGUAGE "English"
!insertmacro MUI_LANGUAGE "SimpChinese"

LangString CageDirectoryHelp ${LANG_ENGLISH} "Choose where to install the Cage application. On first launch, you can choose a separate drive for the Linux image, download cache, and virtual disks before starting the environment."
LangString CageDirectoryHelp ${LANG_SIMPCHINESE} "请选择 Cage 程序的安装目录。首次打开 Cage 后，可以在启动环境前单独选择存放 Linux 镜像、下载缓存和虚拟磁盘的磁盘。"

Var UpdateMode
Var StageDir
Var BackupDir
Var ExistingInstall

!macro CheckStopped FILE
  ${If} ${FileExists} "$INSTDIR\${FILE}"
    ; Exclusive write access also detects images whose VM/controller has no window.
    System::Call 'kernel32::CreateFileW(w "$INSTDIR\${FILE}", i 0x40000000, i 0, p 0, i 3, i 0, p 0) p .r0'
    ${If} $0 == -1
      SetErrorLevel 2
      Abort "Quit Cage and stop its environment before installing or uninstalling."
    ${EndIf}
    System::Call 'kernel32::CloseHandle(p r0)'
  ${EndIf}
!macroend

Function .onInit
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
  IfFileExists "$INSTDIR\*.*" foreign done
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
FunctionEnd

Section "Cage" Main
  Call CheckDestination
  ${GetParent} "$INSTDIR" $0
  CreateDirectory "$0"
  GetTempFileName $StageDir "$0"
  Delete "$StageDir"
  CreateDirectory "$StageDir"
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
  SetRegView 32
  ReadRegStr $0 HKLM "SOFTWARE\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_APP_ID}" "pv"
  ReadRegStr $1 HKCU "Software\Microsoft\EdgeUpdate\Clients\${WEBVIEW2_APP_ID}" "pv"
  SetRegView 64
  ${If} $0 == ""
  ${OrIf} $0 == "0.0.0.0"
    ${If} $1 == ""
    ${OrIf} $1 == "0.0.0.0"
      DetailPrint "Installing the bundled Microsoft WebView2 runtime..."
      ExecWait '"$StageDir\resources\runtime\MicrosoftEdgeWebView2RuntimeInstallerX64.exe" /silent /install' $0
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
    ClearErrors
    Rename "$INSTDIR" "$BackupDir"
    ${If} ${Errors}
      RMDir /r "$StageDir"
      SetErrorLevel 2
      Abort "Could not move the installed application. Quit Cage and retry."
    ${EndIf}
  ${Else}
    RMDir "$INSTDIR"
  ${EndIf}
  ClearErrors
  Rename "$StageDir" "$INSTDIR"
  ${If} ${Errors}
    ${If} $BackupDir != ""
      ClearErrors
      Rename "$BackupDir" "$INSTDIR"
      ${If} ${Errors}
        RMDir /r "$StageDir"
        SetErrorLevel 2
        Abort "Installation failed. The previous application is preserved at $BackupDir; restore that folder before retrying."
      ${EndIf}
    ${EndIf}
    RMDir /r "$StageDir"
    SetErrorLevel 2
    Abort "Could not install Cage. The previous installation has been restored."
  ${EndIf}
  ${If} $BackupDir != ""
    ; BackupDir came from the verified Cage installation above. User data lives elsewhere.
    RMDir /r "$BackupDir"
  ${EndIf}
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayName" "Cage"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "Publisher" "Cage contributors"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKCU "${UNINSTALL_KEY}" "UninstallString" '$\"$INSTDIR\Uninstall.exe$\"'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "QuietUninstallString" '$\"$INSTDIR\Uninstall.exe$\" /S'
  WriteRegStr HKCU "${UNINSTALL_KEY}" "DisplayIcon" "$INSTDIR\Cage.exe,0"
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoModify" 1
  WriteRegDWORD HKCU "${UNINSTALL_KEY}" "NoRepair" 1
  CreateDirectory "$SMPROGRAMS\Cage"
  CreateShortcut "$SMPROGRAMS\Cage\Cage.lnk" "$INSTDIR\Cage.exe"
  CreateShortcut "$SMPROGRAMS\Cage\Uninstall Cage.lnk" "$INSTDIR\Uninstall.exe"
  SetErrorLevel 0
SectionEnd

Function un.onInit
  SetShellVarContext current
  SetRegView 64
  FileOpen $0 "$INSTDIR\Cage.install-id" r
  FileRead $0 $1
  FileClose $0
  ${If} $1 != "${APP_ID}"
    SetErrorLevel 2
    Abort "This is not a Cage installation folder."
  ${EndIf}
  !insertmacro CheckStopped "Cage.exe"
  !insertmacro CheckStopped "resources\cage.exe"
  !insertmacro CheckStopped "resources\mihomo.exe"
  !insertmacro CheckStopped "resources\runtime\qemu\qemu-system-x86_64.exe"
FunctionEnd

Section "Uninstall"
  Delete "$SMPROGRAMS\Cage\Cage.lnk"
  Delete "$SMPROGRAMS\Cage\Uninstall Cage.lnk"
  RMDir "$SMPROGRAMS\Cage"
  DeleteRegKey HKCU "${UNINSTALL_KEY}"
  ; Delete only files shipped in this build. Preserve user-created files and all external data.
  !include "${UNINSTALL_MANIFEST}"
  Delete "$INSTDIR\Uninstall.exe"
  RMDir "$INSTDIR"
SectionEnd
