; SPDX-License-Identifier: AGPL-3.0-only
; The steps of the installer of Windows (specs/2026-11-13-desk-updates.md UB2.4; specs/2026-11-15-desk-mcp.md MC1.8). The
; installer checks only the main program; the backend, PostgreSQL and codex-app-server of the installation hold its files
; too, and so does the program of the bridge for the programs of the author, which Claude Code, Claude Desktop or Codex run
; and never stop by themselves. Before the files are copied, and before the app is removed, the step first ends the
; programs of the bridge of the installation, then waits up to half a minute for every other program that runs from the
; directory of the installation and ends the ones still running — an update stops them before, and this keeps an
; installation by hand over a running app whole. Removed with «Удалить данные приложения», the app takes away the directory
; of the sockets of the bridge in the home of the user too. A directory reaches PowerShell by the environment of the
; installer, never inside its command: a name with an apostrophe, a dollar sign or a backtick stays a name. The variables
; are gone before the installer starts the app, and $0 is the caller's again.
; The installer is a 32-bit program (the template of the bundler sets no Target, so makensis builds x86-unicode), and the
; powershell.exe it finds by its name is the 32-bit one of SysWOW64, whose Get-Process gives no path of a 64-bit process.
; So the step runs the 64-bit PowerShell where the system has one (Sysnative), finds the processes by the path CIM gives
; (Win32_Process.ExecutablePath, whatever the bitness of the caller) and ends them by their number
; (specs/2026-11-15-desk-ci-shell.md CS5.1).
!macro CANONKEEPER_STOP_PROGRAMS
  Push $0
  StrCpy $0 "$INSTDIR\"
  System::Call 'Kernel32::SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", t r0)'
  StrCpy $0 "powershell.exe"
  ${If} ${FileExists} "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
    StrCpy $0 "$WINDIR\Sysnative\WindowsPowerShell\v1.0\powershell.exe"
  ${EndIf}
  nsExec::Exec `"$0" -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "$$d = $$env:CANONKEEPER_INSTALL_DIR; $$bridge = [IO.Path]::Combine($$d, 'canonkeeper-mcp.exe'); function Held { @(Get-CimInstance -ClassName Win32_Process | Where-Object { $$_.ProcessId -ne $$PID -and $$_.ExecutablePath -and $$_.ExecutablePath.StartsWith($$d, [StringComparison]::OrdinalIgnoreCase) }) }; Held | Where-Object { [string]::Equals($$_.ExecutablePath, $$bridge, [StringComparison]::OrdinalIgnoreCase) } | ForEach-Object { Stop-Process -Id $$_.ProcessId -Force }; for ($$i = 0; $$i -lt 30; $$i++) { $$held = @(Held); if ($$held.Count -eq 0) { exit 0 }; Start-Sleep -Seconds 1 }; $$held | ForEach-Object { Stop-Process -Id $$_.ProcessId -Force }"`
  ; The code of PowerShell: the step does what it can, and a file still held is the installer's own refusal.
  Pop $0
  System::Call 'Kernel32::SetEnvironmentVariable(t "CANONKEEPER_INSTALL_DIR", p 0)'
  Pop $0
!macroend

!macro NSIS_HOOK_PREINSTALL
  !insertmacro CANONKEEPER_STOP_PROGRAMS
!macroend

!macro NSIS_HOOK_PREUNINSTALL
  !insertmacro CANONKEEPER_STOP_PROGRAMS
  ${If} $DeleteAppDataCheckboxState = 1
    Push $0
    StrCpy $0 "$PROFILE\.canonkeeper"
    System::Call 'Kernel32::SetEnvironmentVariable(t "CANONKEEPER_SOCKETS_DIR", t r0)'
    nsExec::Exec `powershell.exe -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command "$$s = $$env:CANONKEEPER_SOCKETS_DIR; if (Test-Path -LiteralPath $$s) { Remove-Item -LiteralPath $$s -Recurse -Force }"`
    Pop $0
    System::Call 'Kernel32::SetEnvironmentVariable(t "CANONKEEPER_SOCKETS_DIR", p 0)'
    Pop $0
  ${EndIf}
!macroend
