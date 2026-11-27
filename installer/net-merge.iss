; Inno Setup script. Built by scripts/build.ps1 (needs dist/ filled first):
;   ISCC /DVersion=0.1.0 installer\net-merge.iss
#ifndef Version
  #define Version "0.0.0"
#endif

[Setup]
AppId={{8F2A6C1E-5B3D-4E7A-9C41-2D6B8E0F7A13}
AppName=net-merge
AppVersion={#Version}
AppVerName=net-merge {#Version}
AppPublisher=net-merge contributors
#ifdef RepoUrl
AppPublisherURL={#RepoUrl}
AppSupportURL={#RepoUrl}/issues
AppUpdatesURL={#RepoUrl}/releases
#endif
VersionInfoVersion={#Version}
DefaultDirName={autopf}\net-merge
DefaultGroupName=net-merge
DisableProgramGroupPage=yes
; The TUN adapter and routes need admin, and so does writing to Program Files.
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0.17763
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
LicenseFile=LICENSE
InfoBeforeFile=installer\privacy.txt
SourceDir=..
OutputDir=dist
OutputBaseFilename=net-merge-setup-{#Version}
UninstallDisplayIcon={app}\net-merge.ico
CloseApplications=no
; Uninstall removes %LOCALAPPDATA%\net-merge (status/log files) of the user who installed.
UsedUserAreasWarning=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "ispnames"; Description: "Show ISP names (asks ipwho.is who provides each connection)"; GroupDescription: "Privacy:"
Name: "autostart"; Description: "Start net-merge when I sign in (no UAC prompt)"; GroupDescription: "Startup:"; Flags: unchecked
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Shortcuts:"; Flags: unchecked

[Files]
Source: "dist\net-merge.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\net-merge-tray.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\wintun.dll"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\wintun-LICENSE.txt"; DestDir: "{app}"; Flags: ignoreversion
Source: "dist\net-merge.ico"; DestDir: "{app}"; Flags: ignoreversion
Source: "LICENSE"; DestDir: "{app}"; Flags: ignoreversion
Source: "THIRD-PARTY-NOTICES.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion isreadme

[Icons]
Name: "{autoprograms}\net-merge"; Filename: "{app}\net-merge-tray.exe"; IconFilename: "{app}\net-merge.ico"; Comment: "Use all your network adapters at once"
Name: "{autodesktop}\net-merge"; Filename: "{app}\net-merge-tray.exe"; IconFilename: "{app}\net-merge.ico"; Tasks: desktopicon

[Run]
; Silent installs can pass /MERGETASKS=!ispnames. It runs twice, because the settings are per Windows
; account: once as the signed-in user, and once as the account Setup runs as. They are the same account
; unless a standard user elevated with a different admin account, and then the tray, which runs elevated,
; reads the admin account's settings and would never see the choice. The second run is harmless otherwise.
Filename: "{app}\net-merge.exe"; Parameters: "isp-names off"; Flags: runhidden runasoriginaluser; Check: TurnIspNamesOff
Filename: "{app}\net-merge.exe"; Parameters: "isp-names off"; Flags: runhidden; Check: TurnIspNamesOff
Filename: "{app}\net-merge.exe"; Parameters: "autostart on"; Flags: runhidden; Tasks: autostart; StatusMsg: "Enabling start at sign-in..."
; The installer is already elevated, so the tray starts without a second UAC prompt.
Filename: "{app}\net-merge-tray.exe"; Description: "Start net-merge now"; Flags: nowait postinstall skipifsilent

[UninstallRun]
Filename: "{app}\net-merge.exe"; Parameters: "autostart off"; Flags: runhidden; RunOnceId: "AutostartOff"

[UninstallDelete]
Type: filesandordirs; Name: "{localappdata}\net-merge"

[Code]
// The tick box is applied when it is newly unticked, not on every upgrade: Setup remembers the
// last choice, and someone may have switched the names back on in the tray since.
var PreviousIspNames: String;

procedure InitializeWizard;
begin
  PreviousIspNames := GetPreviousData('IspNames', 'on');
end;

procedure RegisterPreviousData(PreviousDataKey: Integer);
begin
  if WizardIsTaskSelected('ispnames') then
    SetPreviousData(PreviousDataKey, 'IspNames', 'on')
  else
    SetPreviousData(PreviousDataKey, 'IspNames', 'off');
end;

function TurnIspNamesOff: Boolean;
begin
  Result := (not WizardIsTaskSelected('ispnames')) and (PreviousIspNames <> 'off');
end;

// Turn net-merge off and make sure no copy is running before files are replaced or removed.
// Killing it is safe: the TUN adapter and its routes disappear with the process.
procedure StopNetMerge;
var
  rc: Integer;
begin
  if FileExists(ExpandConstant('{app}\net-merge.exe')) then
    Exec(ExpandConstant('{app}\net-merge.exe'), 'quit', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM net-merge-tray.exe', '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM net-merge.exe', '', SW_HIDE, ewWaitUntilTerminated, rc);
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
begin
  StopNetMerge;
  Result := '';
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then
    StopNetMerge;
end;
