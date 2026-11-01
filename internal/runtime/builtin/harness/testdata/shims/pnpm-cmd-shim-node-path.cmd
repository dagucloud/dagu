@SETLOCAL
@IF NOT DEFINED NODE_PATH (
  @SET "NODE_PATH=C:\pnpm\global\5\node_modules;C:\pnpm\global\5\.pnpm\node_modules"
) ELSE (
  @SET "NODE_PATH=C:\pnpm\global\5\node_modules;C:\pnpm\global\5\.pnpm\node_modules;%NODE_PATH%"
)
@IF EXIST "%~dp0\node.exe" (
  "%~dp0\node.exe"  "%~dp0\node_modules\@scope\tool\bin\cli.js" %*
) ELSE (
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  node  "%~dp0\node_modules\@scope\tool\bin\cli.js" %*
)
