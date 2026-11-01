@SETLOCAL
@IF EXIST "%~dp0\cmd.exe" (
  "%~dp0\cmd.exe" /C "%~dp0\node_modules\@scope\tool\bin\cli.cmd" %*
) ELSE (
  @SET PATHEXT=%PATHEXT:;.JS;=;%
  cmd /C "%~dp0\node_modules\@scope\tool\bin\cli.cmd" %*
)
