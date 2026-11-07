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
UninstallDisplayIcon={app}\pencode.exe
OutputDir={#OutputDir}
OutputBaseFilename={#OutputName}
Compression=lzma2
SolidCompression=yes
WizardStyle=modern
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"
Name: "chinesesimplified"; MessagesFile: "compiler:Languages\ChineseSimplified.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#SourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{group}\Pencode"; Filename: "{app}\pencode.exe"
Name: "{autodesktop}\Pencode"; Filename: "{app}\pencode.exe"; Tasks: desktopicon
