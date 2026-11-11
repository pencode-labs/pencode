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
ChangesAssociations=yes

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

[Registry]
Root: HKCU; Subkey: "Software\Classes\Applications\pencode.exe"; ValueType: string; ValueName: "FriendlyAppName"; ValueData: "Pencode"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\Applications\pencode.exe\DefaultIcon"; ValueType: string; ValueData: "{app}\pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\Applications\pencode.exe\shell\open\command"; ValueType: string; ValueData: """{app}\pencode.exe"" -- ""%1"""
Root: HKCU; Subkey: "Software\Classes\Pencode.File"; ValueType: string; ValueData: "Pencode"; Flags: uninsdeletekey
Root: HKCU; Subkey: "Software\Classes\Pencode.File\DefaultIcon"; ValueType: string; ValueData: "{app}\pencode.exe,0"
Root: HKCU; Subkey: "Software\Classes\Pencode.File\shell\open\command"; ValueType: string; ValueData: """{app}\pencode.exe"" -- ""%1"""

; Register alternatives without changing the user's default application.
#define FileExtensions ".txt|.text|.log|.md|.markdown|.mdx|.rst|.adoc|.org|.go|.mod|.sum|.work|.rs|.c|.h|.cc|.cpp|.cxx|.hpp|.hxx|.m|.mm|.js|.jsx|.mjs|.cjs|.ts|.tsx|.mts|.cts|.vue|.svelte|.astro|.html|.htm|.xml|.svg|.css|.scss|.sass|.less|.json|.jsonc|.jsonl|.yaml|.yml|.toml|.ini|.conf|.cfg|.properties|.env|.py|.pyi|.rb|.php|.java|.kt|.kts|.scala|.swift|.dart|.cs|.fs|.fsx|.vb|.sh|.bash|.zsh|.fish|.ps1|.bat|.cmd|.lua|.pl|.pm|.r|.jl|.ex|.exs|.erl|.hrl|.hs|.lhs|.clj|.cljs|.cljc|.edn|.elm|.ml|.mli|.sql|.graphql|.gql|.proto|.csv|.tsv|.tex|.bib|.diff|.patch|.s|.asm|.cmake|.mk|.nix|.tf|.tfvars|.hcl|.lock|"
#sub RegisterFileType
  #define Extension Copy(FileExtensions, 1, Pos("|", FileExtensions) - 1)
Root: HKCU; Subkey: "Software\Classes\Applications\pencode.exe\SupportedTypes"; ValueType: string; ValueName: "{#Extension}"; ValueData: ""
Root: HKCU; Subkey: "Software\Classes\{#Extension}\OpenWithProgids"; ValueType: string; ValueName: "Pencode.File"; ValueData: ""; Flags: uninsdeletevalue uninsdeletekeyifempty
#endsub
#for {FileExtensions = FileExtensions; FileExtensions != ""; FileExtensions = Copy(FileExtensions, Pos("|", FileExtensions) + 1)} RegisterFileType
