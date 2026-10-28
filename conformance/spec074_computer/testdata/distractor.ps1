# A window of another app beside the elements window, for the desktop bench:
# it holds a 保存 button like the elements window's, and writes -Out when
# that button is pressed, so a run that strays out of the app it works in is
# caught.
param([Parameter(Mandatory = $true)][string]$Out)

Add-Type -AssemblyName System.Windows.Forms, System.Drawing

# Keep the project directory free, so the test can remove it.
Set-Location $env:TEMP

$form = New-Object System.Windows.Forms.Form
$form.Text = 'Dagu distractor'
$form.StartPosition = 'Manual'
$form.Location = New-Object System.Drawing.Point(720, 100)
$form.Size = New-Object System.Drawing.Size(300, 160)

$save = New-Object System.Windows.Forms.Button
$save.Name = 'distractorSave'
$save.Text = '保存'
$save.Location = New-Object System.Drawing.Point(100, 50)
$save.Size = New-Object System.Drawing.Size(100, 32)
$save.Add_Click({ Set-Content -Path $Out -Value 'pressed' })
$form.Controls.Add($save)

[System.Windows.Forms.Application]::Run($form)
