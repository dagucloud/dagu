@SETLOCAL
@SET "PATH=C;\tools\bin:%PATH%"
@IF EXIST "%~dp0\node.exe" (
  "%~dp0\node.exe"  "%~dp0\node_modules\@scope\tool\bin\cli.js" %*
) ELSE (
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  node  "%~dp0\node_modules\@scope\tool\bin\cli.js" %*
)
