# Starts the GUI build on a real Windows desktop and checks that the WebView2
# window opens and the local status server answers. Leaves window.png behind.
param([Parameter(Mandatory)][string]$Exe, [string]$Out = "smoke")
$ErrorActionPreference = "Stop"
New-Item -ItemType Directory -Force $Out | Out-Null

$proc = Start-Process -FilePath $Exe -PassThru
$deadline = (Get-Date).AddSeconds(60)
$title = ""
while ((Get-Date) -lt $deadline) {
  $proc.Refresh()
  if ($proc.HasExited) { throw "satellite exited with code $($proc.ExitCode)" }
  $title = $proc.MainWindowTitle
  if ($title -eq "Kumiko Satellite") { break }
  Start-Sleep -Milliseconds 500
}
if ($title -ne "Kumiko Satellite") { throw "no window titled 'Kumiko Satellite' within 60 s (got '$title')" }

# WebView2 renders asynchronously after the window exists.
Start-Sleep -Seconds 8
$res = Invoke-WebRequest -UseBasicParsing -Uri "http://127.0.0.1:8765/" -SkipHttpErrorCheck
Write-Host "status server: HTTP $($res.StatusCode)"
if ($res.StatusCode -ge 500) { throw "status server answered $($res.StatusCode)" }

Add-Type -AssemblyName System.Windows.Forms, System.Drawing
$bounds = [System.Windows.Forms.Screen]::PrimaryScreen.Bounds
$bmp = New-Object System.Drawing.Bitmap $bounds.Width, $bounds.Height
[System.Drawing.Graphics]::FromImage($bmp).CopyFromScreen($bounds.Location, [System.Drawing.Point]::Empty, $bounds.Size)
$bmp.Save((Join-Path $Out "window.png"), [System.Drawing.Imaging.ImageFormat]::Png)

$webview = Get-Process -Name msedgewebview2 -ErrorAction SilentlyContinue
if (-not $webview) { throw "no msedgewebview2 process: the window has no WebView2 content" }
Write-Host "window and WebView2 up ($($webview.Count) webview processes)"
Stop-Process -Id $proc.Id
