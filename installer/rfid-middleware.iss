; CongKong RFID Middleware — Inno Setup 설치 스크립트 (GUI 설계 §8, M4)
; 빌드: ISCC.exe /DAppVersion=<버전> installer\rfid-middleware.iss
; (dist\rfid-middleware.exe 를 먼저 windowsgui 로 교차 빌드해 둘 것)

#ifndef AppVersion
  #define AppVersion "0.0.0-dev"
#endif

#define DataDir "{commonappdata}\CongKong\RFIDMiddleware"
#define ConfigPath DataDir + "\config.json"

[Setup]
AppId={{09D24D16-A73B-4EA2-859C-E0C99431EE0A}
AppName=CongKong RFID Middleware
AppVersion={#AppVersion}
AppPublisher=RevivalLabs · CongKong
DefaultDirName={autopf}\CongKong\RFID Middleware
DefaultGroupName=CongKong RFID Middleware
DisableProgramGroupPage=yes
OutputDir=Output
OutputBaseFilename=rfid-middleware-setup-{#AppVersion}
Compression=lzma2
SolidCompression=yes
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
PrivilegesRequired=admin
WizardStyle=modern
UninstallDisplayName=CongKong RFID Middleware
UninstallDisplayIcon={app}\rfid-middleware.exe
SetupIconFile=congkong.ico

[Languages]
Name: "korean"; MessagesFile: "compiler:Languages\Korean.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Files]
Source: "..\dist\rfid-middleware.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion isreadme
Source: "..\config.example.json"; DestDir: "{app}"; Flags: ignoreversion

[Dirs]
; 데이터 폴더 — 큐 DB·로그·config·status. 설치 사용자(비관리자)도 쓸 수 있게
; Users + Authenticated Users 에 Modify 부여 (권한 문제로 로그·카탈로그·수집이
; 안 되는 것을 방지). [Run] 에서 icacls 로 한 번 더 확실히 부여한다.
Name: "{#DataDir}"; Permissions: users-modify authusers-modify

[Tasks]
Name: "svc"; Description: "무인 상주 서비스로 등록 (행사장에 두고 무인 운영할 때)"; GroupDescription: "설치 옵션:"; Flags: unchecked
Name: "power"; Description: "행사용 전원 설정 적용 (절전·화면 끄기·최대 절전 해제)"; GroupDescription: "설치 옵션:"
Name: "desktopicon"; Description: "바탕화면 바로가기 만들기"; GroupDescription: "설치 옵션:"

[Icons]
Name: "{group}\CongKong RFID 콘솔"; Filename: "{app}\rfid-middleware.exe"; Parameters: "gui --config ""{#ConfigPath}"""; WorkingDir: "{app}"
Name: "{group}\CongKong RFID 콘솔 제거"; Filename: "{uninstallexe}"
Name: "{autodesktop}\CongKong RFID 콘솔"; Filename: "{app}\rfid-middleware.exe"; Parameters: "gui --config ""{#ConfigPath}"""; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
; 데이터 폴더에 사용자 쓰기 권한을 확실히 부여 (icacls — [Dirs] 보완).
Filename: "icacls"; Parameters: """{#DataDir}"" /grant *S-1-5-32-545:(OI)(CI)M /T /C"; Flags: runhidden; StatusMsg: "데이터 폴더 권한 설정 중..."
; 서비스 등록 (옵션). config.json 은 [Code] ssPostInstall 에서 이미 준비됨.
Filename: "{app}\rfid-middleware.exe"; Parameters: "service install --config ""{#ConfigPath}"""; Flags: runhidden waituntilterminated; Tasks: svc; StatusMsg: "서비스를 등록하는 중..."
Filename: "{app}\rfid-middleware.exe"; Parameters: "service start"; Flags: runhidden waituntilterminated; Tasks: svc
; 행사용 전원 설정 (옵션)
Filename: "powercfg"; Parameters: "/change standby-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change monitor-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change hibernate-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change disk-timeout-ac 0"; Flags: runhidden; Tasks: power
; 설치 마침 후 콘솔 열기 (서비스 미등록 시 = GUI 우선 흐름)
Filename: "{app}\rfid-middleware.exe"; Parameters: "gui --config ""{#ConfigPath}"""; Description: "지금 콘솔 열기"; Flags: postinstall nowait skipifsilent; Tasks: not svc

[UninstallRun]
; 서비스 중지·해제. 데이터 폴더(큐·로그)는 보존한다.
Filename: "{app}\rfid-middleware.exe"; Parameters: "service uninstall --config ""{#ConfigPath}"""; Flags: runhidden; RunOnceId: "SvcUninstall"

[Code]
procedure CurStepChanged(CurStep: TSetupStep);
var dataDir, cfg, ex: string;
begin
  if CurStep = ssPostInstall then
  begin
    dataDir := ExpandConstant('{#DataDir}');
    ForceDirectories(dataDir);
    cfg := ExpandConstant('{#ConfigPath}');
    ex := ExpandConstant('{app}\config.example.json');
    // 기존 config 는 보존, 없을 때만 예제를 복사한다 (재설치 안전).
    if (not FileExists(cfg)) and FileExists(ex) then
      FileCopy(ex, cfg, False);
  end;
end;
