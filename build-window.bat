@echo off

REM Build UI assets
echo Building UI assets...

cd ui || exit /b

call pnpm install --frozen-lockfile || exit /b
call pnpm build || exit /b

cd ..

REM Delete all files
del /F /Q "internal\service\frontend\assets\*" 2>nul

REM Delete all subfolders
for /D %%d in ("internal\service\frontend\assets\*") do rmdir /S /Q "%%d"

echo "Copy built files"
xcopy /E /I /Y ui\dist\* internal\service\frontend\assets\

REM set GOOS=windows
REM set GOARCH=amd64
set CGO_ENABLED=0
sc stop dagu
go build -ldflags="-s -w -X main.version=3.0.0" -o C:\usr\bin\dagu.exe ./cmd
sc start dagu
