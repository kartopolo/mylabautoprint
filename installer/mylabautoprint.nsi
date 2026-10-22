; MyLab AutoPrint installer (NSIS 3, Unicode)
; Build: makensis -DVERSION=1.0.0 -DVERSION4=1.0.0.0 -DBUILDDIR=../build -DOUTFILE=../build/MyLabAutoPrint-Setup-1.0.0.exe installer/mylabautoprint.nsi
; Silent install / update: MyLabAutoPrint-Setup-x.y.z.exe /S

Unicode true
SetCompressor /SOLID lzma
ManifestDPIAware true

!ifndef VERSION
  !define VERSION "0.0.0"
!endif
!ifndef VERSION4
  !define VERSION4 "0.0.0.0"
!endif
!ifndef BUILDDIR
  !define BUILDDIR "..\build"
!endif
!ifndef OUTFILE
  !define OUTFILE "${BUILDDIR}\MyLabAutoPrint-Setup-${VERSION}.exe"
!endif

!define APPNAME "MyLab AutoPrint"
!define APPID "MyLabAutoPrint"
!define EXENAME "mylabautoprint.exe"
!define PUBLISHER "MyLab"
!define UNINSTKEY "Software\Microsoft\Windows\CurrentVersion\Uninstall\${APPID}"
!define RUNKEY "Software\Microsoft\Windows\CurrentVersion\Run"

Name "${APPNAME} ${VERSION}"
OutFile "${OUTFILE}"
InstallDir "$PROGRAMFILES64\${APPID}"
InstallDirRegKey HKLM "Software\${APPID}" "InstallDir"
RequestExecutionLevel admin
BrandingText "${APPNAME} ${VERSION}"

VIProductVersion "${VERSION4}"
VIAddVersionKey /LANG=1033 "ProductName" "${APPNAME}"
VIAddVersionKey /LANG=1033 "FileDescription" "${APPNAME} Setup"
VIAddVersionKey /LANG=1033 "CompanyName" "${PUBLISHER}"
VIAddVersionKey /LANG=1033 "FileVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "ProductVersion" "${VERSION}"
VIAddVersionKey /LANG=1033 "LegalCopyright" "${PUBLISHER}"

!include "MUI2.nsh"
!include "x64.nsh"
!include "LogicLib.nsh"
!include "FileFunc.nsh"

!define MUI_ICON "icon.ico"
!define MUI_UNICON "icon.ico"
!define MUI_ABORTWARNING
!define MUI_FINISHPAGE_RUN
!define MUI_FINISHPAGE_RUN_TEXT "Jalankan ${APPNAME} sekarang"
!define MUI_FINISHPAGE_RUN_FUNCTION LaunchApp

!insertmacro MUI_PAGE_WELCOME
!insertmacro MUI_PAGE_DIRECTORY
!insertmacro MUI_PAGE_INSTFILES
!insertmacro MUI_PAGE_FINISH
!insertmacro MUI_UNPAGE_CONFIRM
!insertmacro MUI_UNPAGE_INSTFILES

!insertmacro MUI_LANGUAGE "Indonesian"
!insertmacro MUI_LANGUAGE "English"

Var IsUpdate

Function .onInit
  ${IfNot} ${RunningX64}
    MessageBox MB_ICONSTOP "${APPNAME} membutuhkan Windows 64-bit." /SD IDOK
    Abort
  ${EndIf}
  SetRegView 64
  StrCpy $IsUpdate "0"
  ${GetParameters} $R0
  ClearErrors
  ${GetOptions} $R0 "/UPDATE" $R1
  ${IfNot} ${Errors}
    StrCpy $IsUpdate "1"
  ${EndIf}
FunctionEnd

; Start the app de-elevated (as the logged-in user) via explorer.exe
Function LaunchApp
  Exec '"$WINDIR\explorer.exe" "$INSTDIR\${EXENAME}"'
FunctionEnd

Section "Install" SecMain
  SetShellVarContext all
  ; stop a running instance (upgrade / update)
  nsExec::Exec 'taskkill /F /IM ${EXENAME}'
  Pop $0
  Sleep 800

  SetOutPath "$INSTDIR"
  File "${BUILDDIR}\${EXENAME}"
  File "/oname=SumatraPDF.exe" "..\third_party\sumatrapdf\SumatraPDF.exe"
  File "defaults.json"
  File "icon.ico"
  SetOutPath "$INSTDIR\licenses"
  File "/oname=SumatraPDF-COPYING.txt" "..\third_party\sumatrapdf\COPYING.txt"
  File "/oname=SumatraPDF-NOTICE.txt" "..\third_party\sumatrapdf\NOTICE.txt"
  SetOutPath "$INSTDIR"

  ; shared data folder (config + logs) = %ProgramData%\MyLabAutoPrint ($APPDATA with SetShellVarContext all), writable for normal users
  CreateDirectory "$APPDATA\${APPID}"
  nsExec::Exec 'icacls "$APPDATA\${APPID}" /grant *S-1-5-32-545:(OI)(CI)M /T /Q'
  Pop $0

  ; Start Menu
  CreateDirectory "$SMPROGRAMS\${APPNAME}"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\${APPNAME}.lnk" "$INSTDIR\${EXENAME}" "" "$INSTDIR\icon.ico"
  CreateShortcut "$SMPROGRAMS\${APPNAME}\Uninstall ${APPNAME}.lnk" "$INSTDIR\uninstall.exe"

  ; autostart for the installing user (the app also keeps this in sync per user)
  WriteRegStr HKCU "${RUNKEY}" "${APPID}" '"$INSTDIR\${EXENAME}"'

  ; Apps & Features
  WriteUninstaller "$INSTDIR\uninstall.exe"
  WriteRegStr HKLM "Software\${APPID}" "InstallDir" "$INSTDIR"
  WriteRegStr HKLM "Software\${APPID}" "Version" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayName" "${APPNAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayVersion" "${VERSION}"
  WriteRegStr HKLM "${UNINSTKEY}" "Publisher" "${PUBLISHER}"
  WriteRegStr HKLM "${UNINSTKEY}" "DisplayIcon" "$INSTDIR\${EXENAME}"
  WriteRegStr HKLM "${UNINSTKEY}" "InstallLocation" "$INSTDIR"
  WriteRegStr HKLM "${UNINSTKEY}" "UninstallString" '"$INSTDIR\uninstall.exe"'
  WriteRegStr HKLM "${UNINSTKEY}" "QuietUninstallString" '"$INSTDIR\uninstall.exe" /S'
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoModify" 1
  WriteRegDWORD HKLM "${UNINSTKEY}" "NoRepair" 1
  ${GetSize} "$INSTDIR" "/S=0K" $0 $1 $2
  IntFmt $0 "0x%08X" $0
  WriteRegDWORD HKLM "${UNINSTKEY}" "EstimatedSize" "$0"

  ; silent install (auto-update): start the app again automatically
  ${If} ${Silent}
    Call LaunchApp
  ${EndIf}
SectionEnd

Section "Uninstall"
  SetShellVarContext all
  SetRegView 64
  nsExec::Exec 'taskkill /F /IM ${EXENAME}'
  Pop $0
  Sleep 800
  Delete "$INSTDIR\${EXENAME}"
  Delete "$INSTDIR\SumatraPDF.exe"
  Delete "$INSTDIR\defaults.json"
  Delete "$INSTDIR\icon.ico"
  Delete "$INSTDIR\uninstall.exe"
  RMDir /r "$INSTDIR\licenses"
  RMDir "$INSTDIR"
  RMDir /r "$SMPROGRAMS\${APPNAME}"
  DeleteRegValue HKCU "${RUNKEY}" "${APPID}"
  DeleteRegValue HKLM "${RUNKEY}" "${APPID}"
  DeleteRegKey HKLM "${UNINSTKEY}"
  DeleteRegKey HKLM "Software\${APPID}"
  MessageBox MB_YESNO|MB_ICONQUESTION "Hapus juga pengaturan dan log (folder $APPDATA\${APPID})?" /SD IDNO IDNO keep
    RMDir /r "$APPDATA\${APPID}"
  keep:
SectionEnd
