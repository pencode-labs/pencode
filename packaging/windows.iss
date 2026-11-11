; Compile with cmd/pencode-release; all paths and release values are supplied via /D.
[Setup]
AppId={{438863BF-885C-4AC6-A981-9C8F43563985}
AppName=Pencode
AppVersion={#AppVersion}
AppPublisher=Pencode
AppPublisherURL=https://github.com/pencode-labs/pencode
DefaultDirName={localappdata}\Programs\Pencode
DefaultGroupName=Pencode
PrivilegesRequired=lowest
ArchitecturesAllowed={#AppArchitecture}
ArchitecturesInstallIn64BitMode={#AppArchitecture}
DisableDirPage=no
DisableProgramGroupPage=yes
UsePreviousAppDir=yes
UninstallDisplayIcon={app}\Pencode.exe
OutputDir={#OutputDir}
OutputBaseFilename={#OutputName}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no
ChangesAssociations=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Pencode"; Filename: "{app}\Pencode.exe"
Name: "{autodesktop}\Pencode"; Filename: "{app}\Pencode.exe"; Tasks: desktopicon

[Registry]
Root: HKCU; Subkey: "Software\Classes\Applications\Pencode.exe"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "Pencode"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\Applications\Pencode.exe\DefaultIcon"; ValueType: string; ValueData: "{app}\Pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\Applications\Pencode.exe\shell\open\command"; ValueType: string; ValueData: """{app}\Pencode.exe"" -- ""%1"""
Root: HKCU; Subkey: "Software\Classes\Pencode.File"; ValueType: string; ValueData: "Pencode Text and Source File"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\Pencode.File\DefaultIcon"; ValueType: string; ValueData: "{app}\Pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\Pencode.File\shell\open"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "Pencode"
Root: HKCU; Subkey: "Software\Classes\Pencode.File\shell\open\command"; ValueType: string; ValueData: """{app}\Pencode.exe"" -- ""%1"""
Root: HKCU; Subkey: "Software\Classes\*\shell\Pencode"; ValueType: string; ValueData: "Open with Pencode"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\*\shell\Pencode"; ValueType: string; ValueName: "Icon"; ValueData: "{app}\Pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\*\shell\Pencode\command"; ValueType: string; ValueData: """{app}\Pencode.exe"" -- ""%1"""
Root: HKCU; Subkey: "Software\Classes\Directory\shell\Pencode"; ValueType: string; ValueData: "Open with Pencode"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\Directory\shell\Pencode"; ValueType: string; ValueName: "Icon"; ValueData: "{app}\Pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\Directory\shell\Pencode\command"; ValueType: string; ValueData: """{app}\Pencode.exe"" -- ""%1"""
#include AssociationsFile
