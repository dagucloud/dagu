# Copyright (C) 2026 Yota Hamada
# SPDX-License-Identifier: GPL-3.0-or-later
#
# Prints a short summary of TCP port use on a Windows runner. A test that
# fails with WSAENOBUFS found no free ephemeral port; this shows the port
# range and which processes and endpoints held the ports.
#
#   pwsh -File scripts/ci/windows-socket-report.ps1
#
# Run it right after a failed go test, while closed connections still hold
# their ports in TIME_WAIT.

$ErrorActionPreference = 'Continue'

function Write-Top($Title, $Groups, $Limit) {
    Write-Output $Title
    $Groups | Sort-Object Count -Descending | Select-Object -First $Limit |
        ForEach-Object { '{0,7}  {1}' -f $_.Count, $_.Name }
}

Write-Output '::group::TCP port use'
netsh int ipv4 show dynamicport tcp
netsh int ipv4 show excludedportrange protocol=tcp | Select-Object -First 40

$connections = @(Get-NetTCPConnection -ErrorAction SilentlyContinue)
Write-Output "TCP connections: $($connections.Count)"
Write-Top 'By state:' ($connections | Group-Object State) 20

$names = @{}
Get-Process | ForEach-Object { $names[$_.Id] = $_.ProcessName }
$byProcess = $connections | Group-Object {
    $name = $names[[int]$_.OwningProcess]
    if ($name) { $name } else { "pid $($_.OwningProcess)" }
}
Write-Top 'By owning process (top 20):' $byProcess 20

# TIME_WAIT entries belong to no process; the remote endpoint names the server.
$timeWait = $connections | Where-Object State -EQ 'TimeWait' |
    Group-Object { "$($_.RemoteAddress):$($_.RemotePort)" }
Write-Top 'TIME_WAIT by remote endpoint (top 10):' $timeWait 10
Write-Output '::endgroup::'
