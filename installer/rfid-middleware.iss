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
Source: "..\README.md"; DestDir: "{app}"; Flags: ignoreversion
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
; 예전 설치에서 남은 서비스가 부팅 때 몰래 수집을 시작하지 않도록, 옵션과 무관하게
; 항상 정지 + 수동 시작으로 되돌린다 (서비스가 없으면 no-op). 이래야 최초 실행이
; "수집 대기" 로 깨끗하게 뜬다.
Filename: "{app}\rfid-middleware.exe"; Parameters: "service reset"; Flags: runhidden waituntilterminated; StatusMsg: "기존 서비스 상태 정리 중..."
; 서비스 등록 (옵션). config.json 은 [Code] ssPostInstall 에서 이미 준비됨.
; 서비스는 등록만 한다(자동 시작하지 않는다). 최초 실행은 사람이 콘솔을 열어
; 세션을 설정하고 수집을 켠다. 서비스는 이후 재부팅부터 무인 자동 시작한다.
Filename: "{app}\rfid-middleware.exe"; Parameters: "service install --config ""{#ConfigPath}"""; Flags: runhidden waituntilterminated; Tasks: svc; StatusMsg: "서비스를 등록하는 중..."
; 행사용 전원 설정 (옵션)
Filename: "powercfg"; Parameters: "/change standby-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change monitor-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change hibernate-timeout-ac 0"; Flags: runhidden; Tasks: power
Filename: "powercfg"; Parameters: "/change disk-timeout-ac 0"; Flags: runhidden; Tasks: power
; 설치 마침 후 콘솔 열기 — 최초 실행은 사람이 콘솔에서 세션 설정·수집 시작
Filename: "{app}\rfid-middleware.exe"; Parameters: "gui --config ""{#ConfigPath}"""; Description: "지금 콘솔 열기 (세션 설정·수집 시작)"; Flags: postinstall nowait skipifsilent

[UninstallRun]
; 서비스 중지·해제. 데이터 폴더(큐·로그)는 보존한다.
Filename: "{app}\rfid-middleware.exe"; Parameters: "service uninstall --config ""{#ConfigPath}"""; Flags: runhidden; RunOnceId: "SvcUninstall"

[Code]
// 파일 복사 전에 실행 중인 서비스를 멈춘다. 서비스가 {app}\rfid-middleware.exe 를
// 잠그고 있으면 새 exe 로 교체할 수 없고 [Run] 의 'service reset' 도 옛 exe 를
// 실행하게 된다. sc.exe 로 정지 + 수동 시작으로 되돌려 부팅 자동 수집을 없앤다.
function PrepareToInstall(var NeedsRestart: Boolean): String;
var rc: Integer;
begin
  Exec(ExpandConstant('{sys}\sc.exe'), 'stop ' + 'CongKongRFIDMiddleware',
    '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\sc.exe'), 'config ' + 'CongKongRFIDMiddleware start= demand',
    '', SW_HIDE, ewWaitUntilTerminated, rc);
  Result := '';
end;

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
