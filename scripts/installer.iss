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
#define ServiceWrapper "dagu-service.exe"
#define ServiceConfig "dagu-service.xml"
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
OutputBaseFilename=dagu-{#AppVersion}-setup
Uninstallable=yes
UninstallDisplayIcon={app}\{#AppExeName}
WizardStyle=modern

[Tasks]
Name: "path"; Description: "Add Dagu to the system PATH"; GroupDescription: "Additional options:"
Name: "service"; Description: "Install Dagu as a Windows service (runs start-all in the background)"; GroupDescription: "Background service:"
Name: "startall"; Description: "Add a Dagu start-all shortcut (not needed when the service is installed)"; GroupDescription: "Dagu commands:"; Flags: unchecked
Name: "server"; Description: "Add a Dagu server shortcut"; GroupDescription: "Dagu commands:"
Name: "scheduler"; Description: "Add a Dagu scheduler shortcut"; GroupDescription: "Dagu commands:"
Name: "coordinator"; Description: "Add a Dagu coordinator shortcut"; GroupDescription: "Dagu commands:"
Name: "worker"; Description: "Add a Dagu worker shortcut"; GroupDescription: "Dagu commands:"

[Files]
Source: "{#BinaryPath}"; DestDir: "{app}"; DestName: "{#AppExeName}"; Flags: ignoreversion
Source: "scripts\installer.ps1"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Dagu start-all"; Filename: "{app}\{#AppExeName}"; Parameters: "start-all"; WorkingDir: "{app}"; Tasks: startall
Name: "{group}\Dagu server"; Filename: "{app}\{#AppExeName}"; Parameters: "server"; WorkingDir: "{app}"; Tasks: server
Name: "{group}\Dagu scheduler"; Filename: "{app}\{#AppExeName}"; Parameters: "scheduler"; WorkingDir: "{app}"; Tasks: scheduler
Name: "{group}\Dagu coordinator"; Filename: "{app}\{#AppExeName}"; Parameters: "coordinator"; WorkingDir: "{app}"; Tasks: coordinator
Name: "{group}\Dagu worker"; Filename: "{app}\{#AppExeName}"; Parameters: "worker"; WorkingDir: "{app}"; Tasks: worker

[UninstallDelete]
Type: files; Name: "{app}\{#ServiceConfig}.*.bak"

[Code]
const
  EnvironmentKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';
  InstallerKey = 'Software\Dagu\InnoSetup';
  PathMarker = 'SystemPathAdded';
  ServiceWrapper = '{#ServiceWrapper}';
  InstallerScript = 'installer.ps1';

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

function PowerShellPath: string;
begin
  { Setup runs as a 32-bit process, so resolving by name alone can reach the
    WOW64 PowerShell, which reports the x86 Program Files directory. }
  Result := ExpandConstant('{sys}\WindowsPowerShell\v1.0\powershell.exe');
  if not FileExists(Result) then begin
    Result := 'powershell.exe';
  end;
end;

function InstallerScriptArgs(const Extra: string): string;
begin
  Result := '-NoProfile -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\') + InstallerScript + '" -NoPrompt -ServiceOnly ' +
    Extra + ' -InstallDir "' + ExpandConstant('{app}') + '"';
end;

function RetryCommand: string;
begin
  Result := 'powershell -ExecutionPolicy Bypass -File "' +
    ExpandConstant('{app}\') + InstallerScript +
    '" -ServiceOnly -Service yes -Port {#DefaultPort}' +
    ' -CoordinatorPort {#DefaultCoordinatorPort}' +
    ' -InstallDir "' + ExpandConstant('{app}') + '"';
end;

procedure InstallService;
var
  ResultCode: Integer;
begin
  if not Exec(PowerShellPath,
              InstallerScriptArgs('-Service yes -ServiceScope system -OpenBrowser no'),
              ExpandConstant('{app}'), SW_HIDE, ewWaitUntilTerminated, ResultCode) then begin
    Log('Dagu service setup could not be started: ' + SysErrorMessage(ResultCode));
    SuppressibleMsgBox(
      'Dagu was installed, but the Windows service could not be configured.' + #13#10#13#10 +
      SysErrorMessage(ResultCode) + #13#10#13#10 +
      'Retry from an elevated PowerShell prompt:' + #13#10 + RetryCommand,
      mbError, MB_OK, IDOK);
    exit;
  end;
  if ResultCode <> 0 then begin
    Log('Dagu service setup failed with exit code ' + IntToStr(ResultCode) + '.');
    SuppressibleMsgBox(
      'Dagu was installed, but the Windows service setup failed (exit code ' +
      IntToStr(ResultCode) + ').' + #13#10#13#10 +
      'Common causes are no network access to download the service wrapper, ' +
      'or port {#DefaultPort} or {#DefaultCoordinatorPort} already being in use. ' +
      'Retry from an elevated PowerShell prompt to see the actual error, ' +
      'changing -Port or -CoordinatorPort if needed:' + #13#10 + RetryCommand,
      mbError, MB_OK, IDOK);
  end;
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
  if (not Exec(PowerShellPath, InstallerScriptArgs('-Uninstall'),
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
