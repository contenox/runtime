[CmdletBinding()]
param(
    [ValidateSet('12.9', '13.4')]
    [string]$CudaVersion = '12.9',
    [switch]$PublishCompletedDependenciesOnly
)

$ErrorActionPreference = 'Stop'
$runtimeRoot = Split-Path -Parent $PSScriptRoot
$make = (Get-Command make.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1).Source
if (-not $make) { $make = 'C:\msys64\usr\bin\make.exe' }
if (-not (Test-Path -LiteralPath $make -PathType Leaf)) { throw 'GNU Make is required. Put make.exe on PATH.' }
$git = Get-Command git.exe -CommandType Application -ErrorAction SilentlyContinue | Select-Object -First 1
$shellCandidates = @(
    (Join-Path (Split-Path -Parent $make) 'bash.exe')
    'C:\msys64\usr\bin\bash.exe'
    if ($git) { Join-Path (Split-Path -Parent (Split-Path -Parent $git.Source)) 'usr\bin\bash.exe' }
)
$bash = $shellCandidates | Where-Object { Test-Path -LiteralPath $_ -PathType Leaf } | Select-Object -First 1
if (-not $bash) { throw 'Native Bash from MSYS2 or Git for Windows is required.' }
$shellBin = Split-Path -Parent $bash
$bash = $bash.Replace('\', '/')
$aws = 'C:\Program Files\Amazon\AWSCLIV2\aws.exe'

if ($PublishCompletedDependenciesOnly -and [string]::IsNullOrWhiteSpace($env:MODELD_DEPS_S3_URI)) {
    throw 'Set MODELD_DEPS_S3_URI to the dependency store for publication'
}

function Resolve-NativePython {
    $candidates = [System.Collections.Generic.List[string]]::new()
    foreach ($name in @('python3.exe', 'python.exe')) {
        foreach ($command in @(Get-Command $name -All -ErrorAction SilentlyContinue)) {
            if ($command.CommandType -eq 'Application') {
                $candidates.Add($command.Source)
            }
        }
    }
    foreach ($root in @(
        'HKCU:\Software\Python\PythonCore',
        'HKLM:\Software\Python\PythonCore',
        'HKLM:\Software\WOW6432Node\Python\PythonCore'
    )) {
        foreach ($version in @(Get-ChildItem -LiteralPath $root -ErrorAction SilentlyContinue)) {
            $installKey = Join-Path $version.PSPath 'InstallPath'
            $install = Get-ItemProperty -LiteralPath $installKey -ErrorAction SilentlyContinue
            if (-not [string]::IsNullOrWhiteSpace($install.ExecutablePath)) {
                $candidates.Add($install.ExecutablePath)
                continue
            }
            $key = Get-Item -LiteralPath $installKey -ErrorAction SilentlyContinue
            $directory = if ($null -eq $key) { $null } else { $key.GetValue('') }
            if (-not [string]::IsNullOrWhiteSpace($directory)) {
                $candidates.Add((Join-Path $directory 'python.exe'))
            }
        }
    }
    foreach ($candidate in @($candidates | Select-Object -Unique)) {
        if (-not (Test-Path -LiteralPath $candidate -PathType Leaf)) {
            continue
        }
        $probe = (& $candidate -c 'import sys, venv; print(sys.platform)' 2>&1 | Out-String).Trim()
        if ($LASTEXITCODE -eq 0 -and $probe -eq 'win32') {
            return (Resolve-Path -LiteralPath $candidate).Path
        }
    }
    throw 'Native Windows Python with virtual-environment support was not found on PATH or in the PythonCore registry'
}

$nativePython = Resolve-NativePython
$env:PYTHON = $nativePython.Replace('\', '/')

function Invoke-RuntimeMake {
    param([Parameter(ValueFromRemainingArguments)][string[]]$MakeArguments)
    & $make -C $runtimeRoot 'PROJECT_ROOT=./' 'MODELD_ABI_BASELINE=host' "SHELL=$bash" @MakeArguments
    if ($LASTEXITCODE -ne 0) {
        throw "make failed: $($MakeArguments -join ' ')"
    }
}

function Test-RuntimeMake {
    param([Parameter(ValueFromRemainingArguments)][string[]]$MakeArguments)
    & $make -C $runtimeRoot 'PROJECT_ROOT=./' 'MODELD_ABI_BASELINE=host' "SHELL=$bash" @MakeArguments 2>&1 |
        ForEach-Object { Write-Host $_ }
    $exitCode = $LASTEXITCODE
    return $exitCode -eq 0
}

function Enable-ModeldCompiler {
    $env:CC = 'clang.exe'
    $env:CXX = 'clang++.exe'
    $env:MODELD_WINDOWS_TOOLCHAIN = 'msvc'
}

function Stage-MsvcRedistributables {
    param([Parameter(Mandatory)][string]$VisualStudio)
    $redistRoot = Join-Path $VisualStudio 'VC\Redist\MSVC'
    $selected = $null
    foreach ($versionDir in @(Get-ChildItem -LiteralPath $redistRoot -Directory -ErrorAction Stop | Sort-Object Name -Descending)) {
        $x64 = Join-Path $versionDir.FullName 'x64'
        $crt = Get-ChildItem -LiteralPath $x64 -Directory -Filter 'Microsoft.VC*.CRT' -ErrorAction SilentlyContinue | Select-Object -First 1
        $openmp = Get-ChildItem -LiteralPath $x64 -Directory -Filter 'Microsoft.VC*.OpenMP' -ErrorAction SilentlyContinue | Select-Object -First 1
        if ($null -ne $crt -and $null -ne $openmp -and
            (Test-Path -LiteralPath (Join-Path $crt.FullName 'msvcp140.dll')) -and
            (Test-Path -LiteralPath (Join-Path $crt.FullName 'vcruntime140.dll')) -and
            (Test-Path -LiteralPath (Join-Path $crt.FullName 'vcruntime140_1.dll')) -and
            (Test-Path -LiteralPath (Join-Path $openmp.FullName 'vcomp140.dll'))) {
            $selected = @{ Crt = $crt.FullName; OpenMP = $openmp.FullName }
            break
        }
    }
    if ($null -eq $selected) {
        throw "Missing x64 MSVC CRT/OpenMP redistributables below $redistRoot"
    }
    $relative = '.build/modeld-release/msvc-redist'
    $destination = Join-Path $runtimeRoot $relative
    New-Item -ItemType Directory -Force -Path $destination | Out-Null
    foreach ($dll in @('msvcp140.dll', 'vcruntime140.dll', 'vcruntime140_1.dll')) {
        Copy-Item -LiteralPath (Join-Path $selected.Crt $dll) -Destination (Join-Path $destination $dll) -Force
    }
    Copy-Item -LiteralPath (Join-Path $selected.OpenMP 'vcomp140.dll') -Destination (Join-Path $destination 'vcomp140.dll') -Force
    return $relative
}

function Get-BuildPair {
    param([Parameter(Mandatory)][string]$Version)
    switch ($Version) {
        '12.9' {
            @{
                Cuda = 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v12.9'
                VisualStudio = 'C:\Program Files (x86)\Microsoft Visual Studio\2022\BuildTools'
                CompilerPattern = '^19\.(3|4)'
            }
        }
        '13.4' {
            @{
                Cuda = 'C:\Program Files\NVIDIA GPU Computing Toolkit\CUDA\v13.4'
                VisualStudio = 'C:\Program Files\Microsoft Visual Studio\18\Community'
                CompilerPattern = '^19\.5'
            }
        }
    }
}

if ($PublishCompletedDependenciesOnly) {
    $stamp = Join-Path $runtimeRoot '.build\modeld-release\llama-runtime\local\.contenox-runtime-stamp'
    if (-not (Test-Path -LiteralPath $stamp)) {
        throw "Missing completed llama.cpp runtime stamp: $stamp"
    }
    $stampValues = @{}
    foreach ($line in Get-Content -LiteralPath $stamp) {
        $name, $value = $line -split '=', 2
        $stampValues[$name] = $value
    }
    if ($stampValues.cuda -ne 'ON') {
        throw 'The completed llama.cpp runtime was not built with CUDA'
    }
    if ($stampValues.cuda_toolkit -notin @('12.9', '13.4')) {
        throw "Unsupported or missing CUDA toolkit in completed runtime stamp: $($stampValues.cuda_toolkit)"
    }
    $CudaVersion = $stampValues.cuda_toolkit
}

$pair = Get-BuildPair -Version $CudaVersion
if (-not (Test-Path -LiteralPath $pair.VisualStudio)) {
    $vswhere = Join-Path ${env:ProgramFiles(x86)} 'Microsoft Visual Studio\Installer\vswhere.exe'
    if (Test-Path -LiteralPath $vswhere) {
        $range = if ($CudaVersion -eq '13.4') { '[18.0,19.0)' } else { '[17.0,18.0)' }
        $installed = & $vswhere -latest -products '*' -version $range -requires Microsoft.VisualStudio.Component.VC.Tools.x86.x64 -property installationPath
        if ($installed) { $pair.VisualStudio = $installed.Trim() }
    }
}
$msvcRedistDir = if ($PublishCompletedDependenciesOnly) { $null } else { Stage-MsvcRedistributables -VisualStudio $pair.VisualStudio }
$llvmCandidates = @(
    (Join-Path $pair.VisualStudio 'VC\Tools\Llvm\x64\bin'),
    'C:\Program Files\Microsoft Visual Studio\18\Community\VC\Tools\Llvm\x64\bin'
)
$llvmBin = $llvmCandidates | Where-Object {
    (Test-Path -LiteralPath (Join-Path $_ 'clang.exe')) -and
    (Test-Path -LiteralPath (Join-Path $_ 'clang++.exe')) -and
    (Test-Path -LiteralPath (Join-Path $_ 'llvm-readobj.exe')) -and
    (Test-Path -LiteralPath (Join-Path $_ 'llvm-dlltool.exe'))
} | Select-Object -First 1
if ([string]::IsNullOrWhiteSpace($llvmBin)) {
    throw 'Missing the Visual Studio LLVM x64 tools (clang, clang++, llvm-readobj and llvm-dlltool) required by the Windows release ABI'
}
if (-not $PublishCompletedDependenciesOnly) {
    $devShell = Join-Path $pair.VisualStudio 'Common7\Tools\Microsoft.VisualStudio.DevShell.dll'
    if (-not (Test-Path -LiteralPath $devShell)) {
        throw "Missing Visual Studio developer shell: $devShell"
    }
    if (-not (Test-Path -LiteralPath (Join-Path $pair.Cuda 'bin\nvcc.exe'))) {
        throw "Missing CUDA $CudaVersion toolkit: $($pair.Cuda)"
    }
    Import-Module $devShell
    Enter-VsDevShell -VsInstallPath $pair.VisualStudio -Arch amd64 -HostArch amd64 -SkipAutomaticLocation
    $env:CUDA_PATH = $pair.Cuda
    $env:Path = "$(Join-Path $pair.Cuda 'bin');$env:Path"
    $env:LLAMA_CPP_EXTRA_CMAKE_FLAGS = '-G Ninja -DCMAKE_C_COMPILER=cl.exe -DCMAKE_CXX_COMPILER=cl.exe'
} else {
    $nvcc = Join-Path $pair.Cuda 'bin\nvcc.exe'
    if (-not (Test-Path -LiteralPath $nvcc)) {
        throw "Missing CUDA $CudaVersion toolkit recorded by the completed runtime: $($pair.Cuda)"
    }
    $env:CUDA_PATH = $pair.Cuda
    $env:Path = "$(Join-Path $pair.Cuda 'bin');$env:Path"
}

if (-not $PublishCompletedDependenciesOnly) {
    $clCommand = Get-Command cl.exe -ErrorAction Stop
    Get-Command cmake.exe -ErrorAction Stop | Out-Null
    Get-Command ninja.exe -ErrorAction Stop | Out-Null
    $clVersion = (& $clCommand.Source 2>&1 | Out-String)
    if ($clVersion -notmatch 'Compiler Version (?<Version>\d+\.\d+)') {
        throw "Cannot read the Visual Studio host compiler version: $($clVersion.Trim())"
    }
    if ($Matches.Version -notmatch $pair.CompilerPattern) {
        throw "Visual Studio host compiler does not match CUDA ${CudaVersion}: $($Matches.Version)"
    }
}
$goCommand = Get-Command go.exe -ErrorAction Stop
$goVersion = (& $goCommand.Source env GOVERSION).Trim()
if ($goVersion -notmatch '^go(?<Version>\d+\.\d+(?:\.\d+)?)$' -or [version]$Matches.Version -lt [version]'1.27') {
    throw "Windows release packaging requires Go 1.27 or newer for the MSVC-targeting cgo path; found $goVersion"
}
$clangTarget = (& (Join-Path $llvmBin 'clang.exe') -dumpmachine).Trim()
if ($clangTarget -ne 'x86_64-pc-windows-msvc') {
    throw "Windows release LLVM compiler has the wrong target: $clangTarget"
}
$nvccCommand = Get-Command nvcc.exe -ErrorAction Stop
$nvccVersion = (& $nvccCommand.Source --version 2>&1 | Out-String)
if ($nvccVersion -notmatch "release $([regex]::Escape($CudaVersion))([,\s]|$)") {
    throw "Selected nvcc does not report CUDA ${CudaVersion}: $($nvccVersion.Trim())"
}

$pathParts = @($env:Path -split ';' | Where-Object {
    $_ -and $_ -notin @($shellBin, 'C:\msys64\ucrt64\bin')
})
$awsDirectory = Split-Path -Parent $aws
if ($awsDirectory -notin $pathParts) {
    $pathParts = @($awsDirectory) + $pathParts
}
$system32 = [Array]::FindIndex($pathParts, [Predicate[string]]{
    param($entry)
    $entry -ieq "$env:SystemRoot\System32"
})
if ($system32 -lt 0) {
    $pathParts += $shellBin
} elseif ($system32 -eq 0) {
    $pathParts = @($shellBin) + $pathParts
} else {
    $pathParts = @($pathParts[0..($system32 - 1)]) + $shellBin + @($pathParts[$system32..($pathParts.Count - 1)])
}
if (Test-Path -LiteralPath 'C:\msys64\ucrt64\bin') { $pathParts += 'C:\msys64\ucrt64\bin' }
$pathParts = @($llvmBin) + @($pathParts | Where-Object { $_ -ine $llvmBin })
$env:Path = $pathParts -join ';'

$env:LLAMA_RUNTIME_ROOT = '.build/modeld-release/llama-runtime'
$env:LLAMA_CPP_BUILD_ROOT = '.build/modeld-release/llama-build'
$env:LLAMA_CPP_REF_DIR = '.build/modeld-release/llama-src'
$env:OPENVINO_WORKDIR = '.build/modeld-release/openvino'
$env:OPENVINO_GENAI_REPO = '.build/modeld-release/openvino-genai'
$env:MODELD_RELEASE_OPENVINO = '1'
$depsOut = '.build/modeld-release/windows-deps'
$pullDir = '.build/p'

if ($env:MODELD_DEPS_S3_URI -like 's3://*') {
    & $aws s3 ls "$($env:MODELD_DEPS_S3_URI.TrimEnd('/'))/" *> $null
    if ($LASTEXITCODE -ne 0) {
        throw "Cannot access dependency store: $env:MODELD_DEPS_S3_URI"
    }
}

if (-not $PublishCompletedDependenciesOnly) {
    if ($env:MODELD_DEPS_S3_URI -and (Test-RuntimeMake check-modeld-deps-store)) {
        Enable-ModeldCompiler
        Invoke-RuntimeMake package-modeld-prebuilt "MODELD_PULL_DIR=$pullDir" "MODELD_MSVC_REDIST_DIR=$msvcRedistDir"
        exit 0
    }
    Invoke-RuntimeMake deps-modeld
    Invoke-RuntimeMake build-llamacpp-runtime
}

Invoke-RuntimeMake check-modeld-openvino-deps
Invoke-RuntimeMake bundle-modeld-deps "MODELD_DEPS_OUT=$depsOut"

$envFiles = @(Get-ChildItem -LiteralPath (Join-Path $runtimeRoot $depsOut) -Recurse -Filter bundle.env)
$bundle = $null
foreach ($envFile in $envFiles) {
    $values = @{}
    foreach ($line in Get-Content -LiteralPath $envFile.FullName) {
        $name, $value = $line -split '=', 2
        $values[$name] = $value
    }
    if ($values.MODELD_BUNDLE_PLATFORM -eq 'windows-amd64' -and
        $values.MODELD_BUNDLE_OPENVINO -eq '1' -and
        $values.MODELD_BUNDLE_CUDA -eq '1') {
        if ($null -ne $bundle) {
            throw "More than one Windows CUDA/OpenVINO bundle exists under $depsOut"
        }
        $bundle = $envFile.Directory
    }
}
if ($null -eq $bundle) {
    throw "No Windows CUDA/OpenVINO bundle exists under $depsOut"
}
$bundleRelative = [IO.Path]::GetRelativePath($runtimeRoot, $bundle.FullName).Replace('\', '/')
Invoke-RuntimeMake check-modeld-deps-bundle "MODELD_DEPS_ROOT=$bundleRelative" 'MODELD_RELEASE_OPENVINO=1'
if ($PublishCompletedDependenciesOnly) {
    Invoke-RuntimeMake push-modeld-deps "MODELD_DEPS_OUT=$depsOut"
} else {
    Enable-ModeldCompiler
    Invoke-RuntimeMake package-modeld-release-windows "MODELD_DEPS_ROOT=$bundleRelative" "MODELD_MSVC_REDIST_DIR=$msvcRedistDir"
}
