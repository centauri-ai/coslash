# Install from a release archive

The [README](../README.md#install) covers the normal install paths. Use this page to install a specific version by hand or to verify a download.

## macOS

```sh
VERSION="v0.0.1" # or the desired version tag
ARCH="arm64"  # amd64 on Intel
ASSET="coslash_${VERSION}_darwin_${ARCH}.tar.gz"
BASE_URL="https://github.com/centauri-ai/coslash/releases/download/${VERSION}"
curl -fLO "${BASE_URL}/${ASSET}"
curl -fLO "${BASE_URL}/checksums.txt"
grep -F "  ${ASSET}" checksums.txt | shasum -a 256 -c -
tar -xzf "${ASSET}"
"${ASSET%.tar.gz}/coslash"
```

Release binaries are unsigned. macOS can warn about archives that a browser downloaded. The Homebrew install does not get this warning.

## Windows

Download `checksums-windows.txt` from the same release as `coslash-windows-amd64.exe`. Open Windows PowerShell in the directory that contains both files, then run:

```powershell
$Asset = "coslash-windows-amd64.exe"
$Expected = (Select-String -Path checksums-windows.txt -Pattern "  $([regex]::Escape($Asset))$").Line.Split()[0]
$Actual = (Get-FileHash $Asset -Algorithm SHA256).Hash.ToLowerInvariant()
if ($Actual -ne $Expected) { throw "$Asset checksum does not match" }
```

Windows release binaries are not code-signed, so Windows can show a SmartScreen warning. Do not bypass the security policy of your organization.
