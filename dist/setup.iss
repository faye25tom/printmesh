#define MyAppName      "PrintMesh"
#ifndef MyAppVersion
  #define MyAppVersion "1.0.0"
#endif
#define MyAppPublisher "PrintMesh"
#define ServiceName    "PrintMeshDaemon"
#define DaemonExe      "printmesh-daemon.exe"
#define UIExe          "printmesh-ui.exe"

#ifdef TargetWin7
  #define ArchTag      "win7-x64"
  #define MinWinVer    "6.1sp1"
#else
  #define ArchTag      "win10-11-x64"
  #define MinWinVer    "10.0"
#endif

[Setup]
AppId={{8F3B9C1A-4D2E-4A7B-9E12-6C5A1B8D9E01}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
PrivilegesRequired=admin
MinVersion={#MinWinVer}
#if VER >= 0x06030000
  ArchitecturesAllowed=x64compatible
  ArchitecturesInstallIn64BitMode=x64compatible
#else
  ArchitecturesAllowed=x64
  ArchitecturesInstallIn64BitMode=x64
#endif
OutputDir=output
OutputBaseFilename={#MyAppName}-Setup-{#MyAppVersion}-{#ArchTag}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "chinesesimplified"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "创建桌面快捷方式"; GroupDescription: "附加选项:"
Name: "autostart"; Description: "开机自动启动 {#MyAppName}"; GroupDescription: "附加选项:"

[Files]
Source: "bin\{#DaemonExe}"; DestDir: "{app}"; Flags: ignoreversion
Source: "bin\pdfium.dll";   DestDir: "{app}"; Flags: ignoreversion
Source: "bin\{#UIExe}";     DestDir: "{app}"; Flags: ignoreversion

[Dirs]
Name: "{commonappdata}\{#MyAppName}"

[Icons]
Name: "{autoprograms}\{#MyAppName}"; Filename: "{app}\{#UIExe}"
Name: "{autodesktop}\{#MyAppName}";  Filename: "{app}\{#UIExe}"; Tasks: desktopicon

[Registry]
Root: HKLM; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; \
    ValueType: string; ValueName: "{#MyAppName}UI"; \
    ValueData: """{app}\{#UIExe}"" --minimized"; \
    Flags: uninsdeletevalue; Tasks: autostart

[Run]
Filename: "{app}\{#DaemonExe}"; Parameters: "install"; \
    StatusMsg: "正在安装并启动 {#MyAppName} 后台服务..."; \
    Flags: runhidden waituntilterminated

Filename: "{sys}\sc.exe"; \
    Parameters: "failure {#ServiceName} reset= 86400 actions= restart/5000/restart/10000/restart/30000"; \
    Flags: runhidden waituntilterminated

Filename: "{app}\{#UIExe}"; \
    Description: "立即启动 {#MyAppName}"; \
    Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM {#UIExe}"; \
    Flags: runhidden waituntilterminated; RunOnceId: "KillUI"

Filename: "{sys}\sc.exe"; Parameters: "stop {#ServiceName}"; \
    Flags: runhidden waituntilterminated; RunOnceId: "StopService"
Filename: "{app}\{#DaemonExe}"; Parameters: "uninstall"; \
    Flags: runhidden waituntilterminated; RunOnceId: "UninstallService"

[UninstallDelete]
Type: filesandordirs; Name: "{app}"

[Code]
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
begin
  Result := '';
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM {#UIExe}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
  if Exec(ExpandConstant('{sys}\sc.exe'), 'query {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0) then
  begin
    Exec(ExpandConstant('{sys}\sc.exe'), 'stop {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Sleep(1500);
    Exec(ExpandConstant('{sys}\sc.exe'), 'delete {#ServiceName}', '', SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Sleep(1000);
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  DataDir: String;
begin
  if CurUninstallStep = usPostUninstall then
  begin
    DataDir := ExpandConstant('{commonappdata}\{#MyAppName}');
    if DirExists(DataDir) then
    begin
      if not UninstallSilent then
      begin
        if MsgBox('是否同时删除 {#MyAppName} 的配置、日志以及 Tailscale 节点登录状态？' + #13#10 +
                  '（保留这些数据可方便日后重装时免重新登录）',
                  mbConfirmation, MB_YESNO or MB_DEFBUTTON2) = IDYES then
        begin
          DelTree(DataDir, True, True, True);
        end;
      end;
    end;
  end;
end;
