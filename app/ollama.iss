; Inno Setup Installer for Ollama
;
; To build the installer use the build script invoked from the top of the source tree
; 
; powershell -ExecutionPolicy Bypass -File .\scripts\build_windows.ps


#define MyAppName "Ollama DZ23"
#if GetEnv("PKG_VERSION") != ""
  #define MyAppVersion GetEnv("PKG_VERSION")
#else
  #define MyAppVersion "0.0.0"
#endif
#define MyAppPublisher "LMPrado-DZ23"
#define MyAppURL "https://github.com/LMPrado-DZ23/ollama-dz23"
#define MyAppExeName "ollama app.exe"
#define LlamaServerExeName "llama-server.exe"
#define MyIcon ".\assets\app.ico"

[Setup]
; NOTE: The value of AppId uniquely identifies this application. Do not use the same AppId value in installers for other applications.
; (To generate a new GUID, click Tools | Generate GUID inside the IDE.)
AppId={{A190A09D-6B5D-4ED8-BE01-2F6A78B3D223}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
VersionInfoVersion={#MyAppVersion}
;AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}
ArchitecturesAllowed=x64compatible arm64
ArchitecturesInstallIn64BitMode=x64compatible arm64
DefaultDirName={localappdata}\Programs\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputBaseFilename="OllamaDZ23Setup"
SetupIconFile={#MyIcon}
UninstallDisplayIcon={uninstallexe}
Compression=lzma2/ultra64
LZMAUseSeparateProcess=yes
LZMANumBlockThreads=8
SolidCompression=yes
WizardStyle=modern
ChangesEnvironment=yes
OutputDir=..\dist\

; Disable logging once everything's battle tested
; Filename will be %TEMP%\Setup Log*.txt
SetupLogging=yes
CloseApplications=no
RestartApplications=no
RestartIfNeededByRun=no

; https://jrsoftware.org/ishelp/index.php?topic=setup_wizardimagefile
WizardSmallImageFile=.\assets\setup.bmp

; Ollama requires Windows 10 22H2 or newer for proper unicode rendering
; TODO: consider setting this to 10.0.19045
MinVersion=10.0.10240

; First release that supports WinRT UI Composition for win32 apps
; MinVersion=10.0.17134
; First release with XAML Islands - possible UI path forward
; MinVersion=10.0.18362

; quiet...
DisableDirPage=yes
DisableFinishedPage=yes
DisableReadyMemo=yes
DisableReadyPage=yes
DisableStartupPrompt=yes

; TODO - percentage can't be set less than 100, so how to make it shorter?
; WizardSizePercent=100,80

#if GetEnv("KEY_CONTAINER")
SignTool=MySignTool
SignedUninstaller=yes
#endif

SetupMutex=OllamaSetupMutex

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[LangOptions]
DialogFontSize=12

[Files]
#if FileExists("..\dist\windows-ollama-app-amd64.exe")
Source: "..\dist\windows-ollama-app-amd64.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}" ;Check: not IsArm64();  Flags: ignoreversion 64bit; BeforeInstall: TaskKill('{#MyAppExeName}')
Source: "..\dist\windows-amd64\ollama.exe"; DestDir: "{app}"; Check: not IsArm64(); Flags: ignoreversion 64bit; BeforeInstall: TaskKill('ollama.exe')
Source: "..\dist\windows-amd64\lib\ollama\*"; Excludes: "\mlx_*\*"; DestDir: "{app}\lib\ollama\"; Check: not IsArm64(); Flags: ignoreversion 64bit recursesubdirs
#endif

; For local development, rely on binary compatibility at runtime since we can't cross compile
#if FileExists("..\dist\windows-ollama-app-arm64.exe")
Source: "..\dist\windows-ollama-app-arm64.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}" ;Check: IsArm64();  Flags: ignoreversion 64bit; BeforeInstall: TaskKill('{#MyAppExeName}')
#else 
Source: "..\dist\windows-ollama-app-amd64.exe"; DestDir: "{app}"; DestName: "{#MyAppExeName}" ;Check: IsArm64();  Flags: ignoreversion 64bit; BeforeInstall: TaskKill('{#MyAppExeName}')
#endif

#if FileExists("..\dist\windows-arm64\ollama.exe")
Source: "..\dist\windows-arm64\ollama.exe"; DestDir: "{app}"; Check: IsArm64(); Flags: ignoreversion 64bit; BeforeInstall: TaskKill('ollama.exe')
#endif
#if DirExists("..\dist\windows-arm64\lib\ollama")
Source: "..\dist\windows-arm64\lib\ollama\*"; DestDir: "{app}\lib\ollama\"; Check: IsArm64(); Flags: ignoreversion 64bit recursesubdirs
#endif

Source: ".\assets\app.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\examples\dz23-providers.json"; DestDir: "{userappdata}\Ollama DZ23"; DestName: "dz23-providers.json"; Flags: onlyifdoesntexist
Source: "..\scripts\dz23-configure.ps1"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\app.ico"
Name: "{app}\lib\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\app.ico"
Name: "{userprograms}\{#MyAppName}"; Filename: "{app}\{#MyAppExeName}"; IconFilename: "{app}\app.ico"
Name: "{userprograms}\{#MyAppName} - Configure APIs"; Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\dz23-configure.ps1"""; IconFilename: "{app}\app.ico"

[Run]
Filename: "{cmd}"; Parameters: "/C set ""OLLAMA_DZ23_CONFIG={userappdata}\Ollama DZ23\dz23-providers.json"" & set PATH={app};%PATH% & ""{app}\{#MyAppExeName}"""; Flags: postinstall nowait runhidden

[UninstallRun]
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -Command ""Get-CimInstance Win32_Process | Where-Object {{ $_.ExecutablePath -and $_.ExecutablePath.StartsWith('{app}',[System.StringComparison]::OrdinalIgnoreCase) }} | ForEach-Object {{ Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }}"""; Flags: runhidden
; HACK!  need to give the server and app enough time to exit
; TODO - convert this to a Pascal code script so it waits until they're no longer running, then completes
Filename: "{cmd}"; Parameters: "/c timeout 5"; Flags: runhidden

[UninstallDelete]
Type: filesandordirs; Name: "{userstartup}\{#MyAppName}.lnk"
; Shared Ollama models, history, and official-app data are always preserved.

[InstallDelete]
Type: filesandordirs; Name: "{app}\lib\ollama"

[Messages]
WizardReady=Ollama
ReadyLabel1=%nLet's get you up and running with your own large language models.
SetupAppRunningError=Another Ollama installer is running.%n%nPlease cancel or finish the other installer, then click OK to continue with this install, or Cancel to exit.


;FinishedHeadingLabel=Run your first model
;FinishedLabel=%nRun this command in a PowerShell or cmd terminal.%n%n%n    ollama run llama3.2
;ClickFinish=%n

[Registry]
Root: HKCU; Subkey: "Environment"; \
    ValueType: expandsz; ValueName: "Path"; ValueData: "{olddata};{app}"; \
    Check: NeedsAddPath('{app}')
Root: HKCU; Subkey: "Environment"; ValueType: expandsz; ValueName: "OLLAMA_DZ23_CONFIG"; ValueData: "{userappdata}\Ollama DZ23\dz23-providers.json"; Flags: uninsdeletevalue
; Keep the fork's deep links isolated from an official Ollama installation.
Root: HKCU; Subkey: "Software\Classes\ollama-dz23"; ValueType: string; ValueName: ""; ValueData: "URL:Ollama DZ23 Protocol"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\ollama-dz23"; ValueType: string; ValueName: "URL Protocol"; ValueData: ""; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\ollama-dz23\shell\open\command"; ValueType: string; ValueName: ""; ValueData: """{app}\{#MyAppExeName}"" ""%1"""; Flags: uninsdeletekey

[Code]

function NeedsAddPath(Param: string): boolean;
var
  OrigPath: string;
begin
  if not RegQueryStringValue(HKEY_CURRENT_USER,
    'Environment',
    'Path', OrigPath)
  then begin
    Result := True;
    exit;
  end;
  { look for the path with leading and trailing semicolon }
  { Pos() returns 0 if not found }
  Result := Pos(';' + ExpandConstant(Param) + ';', ';' + OrigPath + ';') = 0;
end;

procedure TaskKill(FileName: String);
var
  ResultCode: Integer;
begin
    { Stop only processes installed under this fork's directory. Image-wide
      taskkill would also terminate an official Ollama installation. }
    Exec('powershell.exe', '-NoProfile -ExecutionPolicy Bypass -Command "Get-CimInstance Win32_Process | Where-Object { $_.ExecutablePath -and $_.ExecutablePath.StartsWith(''''' + ExpandConstant('{app}') + '\'''',[System.StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { Stop-Process -Id $_.ProcessId -Force -ErrorAction SilentlyContinue }"', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
end;
