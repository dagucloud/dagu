# A window for desktop tests: an always-on-top magenta text box that writes
# its text to the file named by -Out whenever the text changes.
param([Parameter(Mandatory = $true)][string]$Out)

Add-Type -AssemblyName System.Windows.Forms, System.Drawing
$Out = [System.IO.Path]::GetFullPath($Out)
# Leave the launch directory so the window does not hold it open.
[System.Environment]::CurrentDirectory = [System.IO.Path]::GetTempPath()

$form = New-Object System.Windows.Forms.Form
$form.Text = 'Dagu desktop test'
$form.StartPosition = 'Manual'
$form.Location = New-Object System.Drawing.Point(100, 100)
$form.Size = New-Object System.Drawing.Size(600, 400)
$form.TopMost = $true

$box = New-Object System.Windows.Forms.TextBox
$box.Multiline = $true
$box.Dock = 'Fill'
$box.BackColor = [System.Drawing.Color]::Magenta
$box.Font = New-Object System.Drawing.Font('Consolas', 24)
$box.Add_TextChanged({ [System.IO.File]::WriteAllText($Out, $box.Text) })
$form.Controls.Add($box)

[void]$form.ShowDialog()
