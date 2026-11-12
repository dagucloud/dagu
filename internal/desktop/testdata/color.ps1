# A window of one color for the capture tests: -TopMost keeps it above every
# window that is not, so it can cover another deterministically.
param(
    [Parameter(Mandatory = $true)][string]$Title,
    [Parameter(Mandatory = $true)][string]$Color,
    [int]$X = 200,
    [int]$Y = 200,
    [int]$Width = 500,
    [int]$Height = 300,
    [switch]$TopMost
)

Add-Type -AssemblyName System.Windows.Forms, System.Drawing

$form = New-Object System.Windows.Forms.Form
$form.Text = $Title
$form.StartPosition = 'Manual'
$form.Location = New-Object System.Drawing.Point($X, $Y)
$form.Size = New-Object System.Drawing.Size($Width, $Height)
$form.BackColor = [System.Drawing.Color]::FromName($Color)
$form.TopMost = $TopMost.IsPresent

[System.Windows.Forms.Application]::Run($form)
