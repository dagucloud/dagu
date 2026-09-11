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
Name: "startall"; Description: "Install Dagu as a Windows service (start-all)"; GroupDescription: "Background service:"
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

[Run]
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\installer.ps1"" -NoPrompt -Service yes -ServiceScope system -Version ""{#AppVersion}"" -InstallDir ""{app}"" -OpenBrowser no"; WorkingDir: "{app}"; StatusMsg: "Installing the Dagu Windows service..."; Tasks: startall; Flags: runhidden waituntilterminated

[UninstallRun]
Filename: "powershell.exe"; Parameters: "-NoProfile -ExecutionPolicy Bypass -File ""{app}\installer.ps1"" -NoPrompt -Uninstall -InstallDir ""{app}"""; WorkingDir: "{app}"; Flags: runhidden waituntilterminated

[Code]
const
  EnvironmentKey = 'SYSTEM\CurrentControlSet\Control\Session Manager\Environment';
  InstallerKey = 'Software\Dagu\InnoSetup';
  PathMarker = 'SystemPathAdded';

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

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if (CurStep = ssPostInstall) and WizardIsTaskSelected('path') then begin
    AddInstallPath;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usUninstall then begin
    RemoveInstallPath;
  end;
end;
