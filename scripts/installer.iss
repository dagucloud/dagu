; Copyright (C) 2026 Yota Hamada
; SPDX-License-Identifier: GPL-3.0-or-later

#ifndef AppVersion
#define AppVersion "0.0.0"
#endif

#ifndef BinaryPath
#define BinaryPath "dist\dagu-windows-amd64.exe"
#endif

#define AppName "Dagu"
#define AppPublisher "Dagu"
#define AppURL "https://github.com/dagucloud/dagu"
#define AppExeName "dagu.exe"
#define AppIcon "dagu.ico"
#define ServiceWrapper "dagu-service.exe"
#define ServiceConfig "dagu-service.xml"
#define ServiceSetupLog "service-setup.log"
; Mirrors the installer.ps1 defaults so the retry command shows the ports it used.
#define DefaultPort "8080"
#define DefaultCoordinatorPort "50055"

[Setup]
SourceDir=..
AppId={{A7E7B5F3-93B2-4D5E-9D7A-5A4A96D2B8A3}
AppName={#AppName}
AppVersion={#AppVersion}
AppPublisher={#AppPublisher}
AppPublisherURL={#AppURL}
AppSupportURL={#AppURL}
AppUpdatesURL={#AppURL}/releases
DefaultDirName={autopf}\Dagu
DefaultGroupName={#AppName}
DisableProgramGroupPage=yes
PrivilegesRequired=admin
ChangesEnvironment=yes
ArchitecturesInstallIn64BitMode=x64compatible
ArchitecturesAllowed=x64compatible
OutputDir=dist
; The name carries no version so the newest installer always has the same
; URL: https://github.com/dagucloud/dagu/releases/latest/download/dagu-windows-amd64-setup.exe
OutputBaseFilename=dagu-windows-amd64-setup
; dagu.exe carries no icon resource, so the setup file, the Start menu entry
; and the Apps & features entry all use the web UI's icon.
SetupIconFile=ui\favicon.ico
Uninstallable=yes
UninstallDisplayIcon={app}\{#AppIcon}
WizardStyle=modern
; Inno Setup 6 skips the welcome page by default; here it carries the one
; paragraph that tells a first-time user what the two run modes are.
DisableWelcomePage=no
#ifdef SignTool
; The release workflow defines this sign tool on the ISCC command line, so the
; installer and its uninstaller carry the same signature as dagu.exe. Builds
; that do not pass /DSignTool stay unsigned.
SignTool={#SignTool}
SignedUninstaller=yes
SignToolRetryCount=3
#endif

[Messages]
WelcomeLabel2=This will install [name/ver] on your computer.%n%nDagu is a workflow engine with a built-in web UI. Setup can run it in the background as a Windows service, so it starts with Windows and is always one click away in your browser, or install just the command-line tool.

; The two run modes are radio buttons (exclusive), so the page asks one plain
; question instead of listing every Dagu process. Silent installs choose with
; /TASKS=service,path or /TASKS=manual,path and may add /PORT=n and
; /COORDINATORPORT=n.
[Tasks]
Name: "service"; Description: "As a Windows service (recommended): starts with Windows, web UI always available in your browser"; GroupDescription: "How should Dagu run?"; Flags: exclusive
Name: "manual"; Description: "Command-line tool only: you start it yourself with ""dagu start-all"" in a terminal"; GroupDescription: "How should Dagu run?"; Flags: exclusive unchecked
Name: "path"; Description: "Let me run ""dagu"" from any terminal window (adds it to the PATH)"; GroupDescription: "Command line:"

[Files]
Source: "{#BinaryPath}"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion
Source: "scripts\installer.ps1"; DestDir: "{app}"; Flags: ignoreversion
Source: "ui\favicon.ico"; DestDir: "{app}"; DestName: "{#AppIcon}"; Flags: ignoreversion

[Icons]
Name: "{group}\Open Dagu"; Filename: "{code:WebUrl}"; IconFilename: "{app}\{#AppIcon}"; Comment: "Open the Dagu web UI in your browser"; Tasks: service

[Run]
Filename: "{code:WebUrl}"; Description: "Open Dagu in your browser"; Flags: postinstall shellexec skipifsilent nowait; Tasks: service; Check: ServiceIsReady

[UninstallDelete]
Type: files; Name: "{app}\{#ServiceConfig}.*.bak"
Type: files; Name: "{app}\{#ServiceSetupLog}"

[Code]
const
  EnvironmentKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';
  InstallerKey = 'Software\Dagu\InnoSetup';
  PathMarker = 'SystemPathAdded';
  ServiceWrapper = '{#ServiceWrapper}';
  ServiceConfig = '{#ServiceConfig}';
  ServiceSetupLog = '{#ServiceSetupLog}';
  InstallerScript = 'installer.ps1';
  ServiceName = 'Dagu';

var
  PortPage: TInputQueryWizardPage;
  PortPagePrefilled: Boolean;
  WebPort, CoordinatorPort: string;
  ServiceSetupFailed: Boolean;

{ ---------------------------------------------------------------------------
  PATH handling
  --------------------------------------------------------------------------- }

function PathHasEntry(const Value, Entry: string): Boolean;
var
  Remaining, Segment: string;
  Separator: Integer;
begin
  Remaining := Value;
  while Remaining <> '' do begin
    Separator := Pos(';', Remaining);
    if Separator = 0 then begin
      Segment := Remaining;
      Remaining := '';
    end else begin
      Segment := Copy(Remaining, 1, Separator - 1);
      Delete(Remaining, 1, Separator);
    end;
    if CompareText(Trim(Segment), Entry) = 0 then begin
      Result := True;
      exit;
    end;
  end;
  Result := False;
end;

function RemovePathEntry(const Value, Entry: string): string;
var
  Remaining, Segment: string;
  Separator: Integer;
begin
  Result := '';
  Remaining := Value;
  while Remaining <> '' do begin
    Separator := Pos(';', Remaining);
    if Separator = 0 then begin
      Segment := Remaining;
      Remaining := '';
    end else begin
      Segment := Copy(Remaining, 1, Separator - 1);
      Delete(Remaining, 1, Separator);
    end;
    if (Trim(Segment) <> '') and (CompareText(Trim(Segment), Entry) <> 0) then begin
      if Result <> '' then begin
        Result := Result + ';';
      end;
      Result := Result + Segment;
    end;
  end;
end;

procedure AddInstallPath;
var
  Path, InstallPath, PreviousPath: string;
  Owned: Boolean;
begin
  InstallPath := ExpandConstant('{app}');
  if not RegQueryStringValue(HKLM, EnvironmentKey, 'Path', Path) then begin
    Path := '';
  end;
  Owned := RegQueryStringValue(HKLM, InstallerKey, PathMarker, PreviousPath);
  if Owned and (CompareText(PreviousPath, InstallPath) <> 0) then begin
    Path := RemovePathEntry(Path, PreviousPath);
    RegWriteExpandStringValue(HKLM, EnvironmentKey, 'Path', Path);
    RegDeleteValue(HKLM, InstallerKey, PathMarker);
    Owned := False;
  end;
  if PathHasEntry(Path, InstallPath) then begin
    if not Owned then begin
      { The entry predates this installer, so it must not be removed on uninstall. }
      RegDeleteValue(HKLM, InstallerKey, PathMarker);
      RegDeleteKeyIfEmpty(HKLM, InstallerKey);
    end;
    exit;
  end;
  if Path <> '' then begin
    Path := Path + ';';
  end;
  RegWriteExpandStringValue(HKLM, EnvironmentKey, 'Path', Path + InstallPath);
  RegWriteStringValue(HKLM, InstallerKey, PathMarker, InstallPath);
end;

procedure RemoveInstallPath;
var
  Path, InstallPath: string;
begin
  if not RegQueryStringValue(HKLM, InstallerKey, PathMarker, InstallPath) then begin
    exit;
  end;
  if RegQueryStringValue(HKLM, EnvironmentKey, 'Path', Path) then begin
    RegWriteExpandStringValue(HKLM, EnvironmentKey, 'Path', RemovePathEntry(Path, InstallPath));
  end;
  RegDeleteValue(HKLM, InstallerKey, PathMarker);
  RegDeleteKeyIfEmpty(HKLM, InstallerKey);
end;

{ ---------------------------------------------------------------------------
  Helpers
  --------------------------------------------------------------------------- }

function PowerShellPath: string;
begin
  { Setup runs as a 32-bit process, so resolving by name alone can reach the
    WOW64 PowerShell, which reports the x86 Program Files directory. }
  Result := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  if not FileExists(Result) then begin
    Result := 'powershell.exe';
  end;
end;

{ Wraps a value as a PowerShell single-quoted literal; a quote inside it is
  doubled, which an install directory chosen by the user may well contain. }
function PsQuote(const Value: string): string;
var
  Escaped: string;
begin
  Escaped := Value;
  StringChangeEx(Escaped, '''', '''''', True);
  Result := '''' + Escaped + '''';
end;

function WebUrl(Param: string): string;
begin
  { The service binds 127.0.0.1, so the address is spelled out rather than
    relying on how a browser resolves localhost. }
  Result := 'http://127.0.0.1:' + WebPort;
end;

function DataDir: string;
begin
  Result := ExpandConstant('{commonappdata}\Dagu');
end;

function ServiceIsReady: Boolean;
begin
  Result := WizardIsTaskSelected('service') and not ServiceSetupFailed;
end;

function ServiceAlreadyInstalled: Boolean;
begin
  Result := FileExists(ExpandConstant('{app}\') + ServiceWrapper);
end;

{ Reads <env name="Name" value="..."/> from a previous dagu-service.xml, so an
  upgrade offers the ports the service already uses. }
function ReadServiceEnv(const Name: string): string;
var
  Lines: TArrayOfString;
  I, P: Integer;
  Line: string;
begin
  Result := '';
  if not LoadStringsFromFile(ExpandConstant('{app}\') + ServiceConfig, Lines) then begin
    exit;
  end;
  for I := 0 to GetArrayLength(Lines) - 1 do begin
    Line := Lines[I];
    if Pos('name="' + Name + '"', Line) = 0 then begin
      continue;
    end;
    P := Pos('value="', Line);
    if P = 0 then begin
      exit;
    end;
    Delete(Line, 1, P + Length('value="') - 1);
    P := Pos('"', Line);
    if P > 0 then begin
      Result := Copy(Line, 1, P - 1);
    end;
    exit;
  end;
end;

function IsValidPort(const Value: string): Boolean;
var
  Number: Integer;
begin
  Number := StrToIntDef(Trim(Value), -1);
  Result := (Number >= 1) and (Number <= 65535);
end;

{ Tries to listen on the port the way the service will, and reports whether
  that worked. Setup itself cannot open sockets, so PowerShell does it. }
function PortIsFree(const Port: string): Boolean;
var
  ResultCode: Integer;
begin
  Result := Exec(PowerShellPath,
    '-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "try { ' +
    '$l = New-Object Net.Sockets.TcpListener([Net.IPAddress]::Loopback, ' + Port + '); ' +
    '$l.Start(); $l.Stop(); exit 0 } catch { exit 1 }"',
    '', SW_HIDE, ewWaitUntilTerminated, ResultCode) and (ResultCode = 0);
end;

{ Picks the line of the service setup log worth showing in a dialog. A
  PowerShell error arrives as a localized multi-line trace; its
  FullyQualifiedErrorId line keeps the thrown message in an English frame,
  so that is preferred over the surrounding location and marker lines. }
function FailureDetail(const FileName: string): string;
var
  Lines: TArrayOfString;
  I, P, Taken: Integer;
  Line, Fallback: string;
begin
  Result := '';
  Fallback := '';
  if not LoadStringsFromFile(FileName, Lines) then begin
    exit;
  end;
  Taken := 0;
  for I := GetArrayLength(Lines) - 1 downto 0 do begin
    Line := Trim(Lines[I]);
    if Line = '' then begin
      continue;
    end;
    P := Pos('FullyQualifiedErrorId : ', Line);
    if P > 0 then begin
      Line := Trim(Copy(Line, P + Length('FullyQualifiedErrorId : '), MaxInt));
      { An exception type id such as CommandNotFoundException,Get-Foo is not a
        message; the plain lines are more useful then. }
      if (Pos(' ', Line) > 0) or (Pos(',', Line) = 0) then begin
        Result := Line;
        exit;
      end;
      continue;
    end;
    { Trace plumbing and the wrapper that merely repeats the exit code. }
    if (Copy(Line, 1, 1) = '+') or (Pos('exited with code', Line) > 0) then begin
      continue;
    end;
    if Fallback = '' then begin
      Fallback := Line;
    end else begin
      Fallback := Line + #13#10 + Fallback;
    end;
    Taken := Taken + 1;
    if Taken >= 2 then begin
      break;
    end;
  end;
  Result := Fallback;
end;

function RetryCommand: string;
begin
  Result := 'powershell -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\') + InstallerScript +
    '" -ServiceOnly -Service yes -Port ' + WebPort +
    ' -CoordinatorPort ' + CoordinatorPort +
    ' -InstallDir "' + ExpandConstant('{app}') + '"';
end;

{ ---------------------------------------------------------------------------
  Wizard pages
  --------------------------------------------------------------------------- }

procedure InitializeWizard;
begin
  { Silent installs never show the page, so the command line is the only
    place their ports can come from. }
  WebPort := Trim(ExpandConstant('{param:PORT|{#DefaultPort}}'));
  CoordinatorPort := Trim(ExpandConstant('{param:COORDINATORPORT|{#DefaultCoordinatorPort}}'));

  PortPage := CreateInputQueryPage(wpSelectTasks,
    'Network ports', 'Which ports should the Dagu service use?',
    'Leave the defaults unless another program on this computer already uses them. ' +
    'The web UI is served on the first port. The second one is used internally by workers.');
  PortPage.Add('Web UI port:', False);
  PortPage.Add('Coordinator port (advanced):', False);
  PortPage.Values[0] := WebPort;
  PortPage.Values[1] := CoordinatorPort;
end;

function ShouldSkipPage(PageID: Integer): Boolean;
begin
  Result := (PageID = PortPage.ID) and not WizardIsTaskSelected('service');
end;

procedure CurPageChanged(CurPageID: Integer);
var
  Previous: string;
begin
  if (CurPageID = PortPage.ID) and not PortPagePrefilled then begin
    { An upgrade keeps the ports the running service already has. }
    PortPagePrefilled := True;
    Previous := ReadServiceEnv('DAGU_PORT');
    if IsValidPort(Previous) then begin
      PortPage.Values[0] := Previous;
    end;
    Previous := ReadServiceEnv('DAGU_COORDINATOR_PORT');
    if IsValidPort(Previous) then begin
      PortPage.Values[1] := Previous;
    end;
  end;

  if CurPageID = wpFinished then begin
    if WizardIsTaskSelected('service') then begin
      if ServiceSetupFailed then begin
        WizardForm.FinishedLabel.Caption :=
          'Dagu is installed, but the Windows service could not be set up. ' +
          'See the message shown earlier, or the log at ' +
          ExpandConstant('{app}\') + ServiceSetupLog + '.';
      end else begin
        WizardForm.FinishedLabel.Caption :=
          'Dagu is installed and running as a Windows service.' + #13#10#13#10 +
          'Web UI:  ' + WebUrl('') + #13#10 +
          'Service:  ' + ServiceName + #13#10 +
          'Data and logs:  ' + DataDir + #13#10#13#10 +
          'On your first visit, create the admin account.';
      end;
    end else if WizardIsTaskSelected('path') then begin
      { start-all binds its own default port, whatever the port page held
        before the user switched to this mode. }
      WizardForm.FinishedLabel.Caption :=
        'Dagu is installed.' + #13#10#13#10 +
        'Open a new terminal window and run:' + #13#10 +
        '    dagu start-all' + #13#10#13#10 +
        'Then visit http://127.0.0.1:{#DefaultPort} in your browser.';
    end else begin
      WizardForm.FinishedLabel.Caption :=
        'Dagu is installed.' + #13#10#13#10 +
        'Open a terminal window and run:' + #13#10 +
        '    "' + ExpandConstant('{app}\{#AppExeName}') + '" start-all' + #13#10#13#10 +
        'Then visit http://127.0.0.1:{#DefaultPort} in your browser.';
    end;
    { The label keeps its designed height unless told to fit the new text,
      and the "Open Dagu" checkbox has to move below it. }
    WizardForm.FinishedLabel.AdjustHeight;
    WizardForm.RunList.Top := WizardForm.FinishedLabel.Top +
      WizardForm.FinishedLabel.Height + ScaleY(16);
  end;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  Port: string;
begin
  Result := True;
  if CurPageID <> PortPage.ID then begin
    exit;
  end;
  WebPort := Trim(PortPage.Values[0]);
  CoordinatorPort := Trim(PortPage.Values[1]);
  if not IsValidPort(WebPort) or not IsValidPort(CoordinatorPort) then begin
    MsgBox('Enter a port number between 1 and 65535 in each field.', mbError, MB_OK);
    Result := False;
    exit;
  end;
  if WebPort = CoordinatorPort then begin
    MsgBox('The web UI and the coordinator need two different ports.', mbError, MB_OK);
    Result := False;
    exit;
  end;
  { A running Dagu service holds its own ports; that is not a conflict. }
  if ServiceAlreadyInstalled then begin
    exit;
  end;
  if not PortIsFree(WebPort) then begin
    Port := WebPort;
  end else if not PortIsFree(CoordinatorPort) then begin
    Port := CoordinatorPort;
  end;
  if Port <> '' then begin
    MsgBox('Port ' + Port + ' is already in use by another program on this computer. ' +
      'Choose a different port, or close the program that uses it and click Next again.',
      mbError, MB_OK);
    Result := False;
  end;
end;

function UpdateReadyMemo(Space, NewLine, MemoUserInfoInfo, MemoDirInfo, MemoTypeInfo,
  MemoComponentsInfo, MemoGroupInfo, MemoTasksInfo: String): String;
begin
  Result := MemoDirInfo + NewLine + NewLine + MemoTasksInfo;
  if WizardIsTaskSelected('service') then begin
    Result := Result + NewLine + NewLine + 'Windows service:' + NewLine +
      Space + 'Web UI: ' + WebUrl('') + NewLine +
      Space + 'Coordinator port: ' + CoordinatorPort + NewLine +
      Space + 'Data and logs: ' + DataDir;
  end;
end;

{ ---------------------------------------------------------------------------
  Service setup
  --------------------------------------------------------------------------- }

procedure InstallService;
var
  ResultCode: Integer;
  LogFile, Detail: string;
begin
  LogFile := ExpandConstant('{app}\') + ServiceSetupLog;
  WizardForm.StatusLabel.Caption := 'Setting up the Dagu Windows service...';
  WizardForm.StatusLabel.Refresh;
  { The script's own output is kept, so a failure can show its actual cause
    instead of a list of guesses. An uncaught error is reduced to its message,
    which otherwise arrives as a localized multi-line trace, and the log is
    written as UTF-8 because LoadStringsFromFile cannot read the UTF-16 that
    PowerShell's own file redirection produces. }
  if not Exec(PowerShellPath,
              '-NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "' +
              '$global:code = 0; & { try { & ' + PsQuote(ExpandConstant('{app}\') + InstallerScript) +
              ' -NoPrompt -ServiceOnly -Service yes -ServiceScope system -OpenBrowser no' +
              ' -Port ' + WebPort + ' -CoordinatorPort ' + CoordinatorPort +
              ' -InstallDir ' + PsQuote(ExpandConstant('{app}')) +
              '; if ($LASTEXITCODE) { $global:code = $LASTEXITCODE } }' +
              ' catch { Write-Output (''Error: '' + $_.Exception.Message); $global:code = 1 } }' +
              ' *>&1 | Out-File -Encoding utf8 -FilePath ' + PsQuote(LogFile) + '; exit $global:code"',
              ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode) then begin
    ServiceSetupFailed := True;
    Log('Dagu service setup could not be started: ' + SysErrorMessage(ResultCode));
    SuppressibleMsgBox(
      'Dagu was installed, but the Windows service setup could not be started.' + #13#10#13#10 +
      SysErrorMessage(ResultCode) + #13#10#13#10 +
      'Retry from an elevated PowerShell prompt:' + #13#10 + RetryCommand,
      mbError, MB_OK, IDOK);
    exit;
  end;
  if ResultCode = 0 then begin
    exit;
  end;
  ServiceSetupFailed := True;
  Log('Dagu service setup failed with exit code ' + IntToStr(ResultCode) + '.');
  Detail := FailureDetail(LogFile);
  if Detail = '' then begin
    Detail := 'The setup script exited with code ' + IntToStr(ResultCode) + '.';
  end;
  SuppressibleMsgBox(
    'Dagu was installed, but the Windows service could not be set up.' + #13#10#13#10 +
    Detail + #13#10#13#10 +
    'The full log is at ' + LogFile + '.' + #13#10 +
    'Once the cause is fixed, retry from an elevated PowerShell prompt:' + #13#10 + RetryCommand,
    mbError, MB_OK, IDOK);
end;

procedure RemoveService;
var
  ResultCode: Integer;
  Wrapper: string;
begin
  Wrapper := ExpandConstant('{app}\') + ServiceWrapper;
  if not FileExists(Wrapper) then begin
    exit;
  end;
  if not FileExists(ExpandConstant('{app}\') + InstallerScript) then begin
    Exec(Wrapper, 'stop', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
    Exec(Wrapper, 'uninstall', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
    exit;
  end;
  if (not Exec(PowerShellPath,
               '-NoProfile -ExecutionPolicy Bypass -File "' + ExpandConstant('{app}\') + InstallerScript +
               '" -NoPrompt -ServiceOnly -Uninstall -InstallDir "' + ExpandConstant('{app}') + '"',
               ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode))
     or (ResultCode <> 0) then begin
    Log('Dagu service removal failed with code ' + IntToStr(ResultCode) + '.');
    SuppressibleMsgBox(
      'The Dagu Windows service could not be removed automatically.' + #13#10#13#10 +
      'Remove it manually from an elevated prompt:' + #13#10 +
      '"' + Wrapper + '" stop' + #13#10 +
      '"' + Wrapper + '" uninstall',
      mbError, MB_OK, IDOK);
  end;
end;

function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  ResultCode: Integer;
  Wrapper: string;
begin
  Result := '';
  { The port page validates what the user typed; a silent install skips the
    page, so its /PORT and /COORDINATORPORT values are checked here instead. }
  if WizardIsTaskSelected('service') and
     (not IsValidPort(WebPort) or not IsValidPort(CoordinatorPort) or (WebPort = CoordinatorPort)) then begin
    Result := '/PORT and /COORDINATORPORT must be two different numbers between 1 and 65535 ' +
      '(got "' + WebPort + '" and "' + CoordinatorPort + '").';
    exit;
  end;
  { A running service holds dagu.exe open, which would block the file copy.
    The wrapper returns only once the service has actually stopped. }
  Wrapper := ExpandConstant('{app}\') + ServiceWrapper;
  if FileExists(Wrapper) then begin
    Exec(Wrapper, 'stop', ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode);
  end;
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep <> ssPostInstall then begin
    exit;
  end;
  if WizardIsTaskSelected('path') then begin
    AddInstallPath;
  end;
  if WizardIsTaskSelected('service') then begin
    InstallService;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    RemoveService;
    RemoveInstallPath;
  end;
end;
