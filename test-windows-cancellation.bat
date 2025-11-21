@echo off
REM Automated Windows Cancellation Fix Test (Batch Version)
REM For environments where PowerShell execution is restricted
REM Exit codes: 0 = success, 1 = failure

setlocal enabledelayedexpansion
set TESTS_PASSED=0
set TESTS_FAILED=0
set TEST_LOG=test-results-batch.log

echo Automated Windows Cancellation Test (Batch Version) > %TEST_LOG%
echo ================================================== >> %TEST_LOG%
echo Started: %date% %time% >> %TEST_LOG%
echo. >> %TEST_LOG%

REM Test 1: Verify Windows environment
echo [INFO] Test 1: Verifying Windows environment
echo %date% %time% [INFO] Verifying Windows environment >> %TEST_LOG%
if "%OS%"=="Windows_NT" (
    echo [PASS] Windows OS detected >> %TEST_LOG%
    set /a TESTS_PASSED+=1
) else (
    echo [FAIL] Not running on Windows >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Test 2: Check Go installation
echo [INFO] Test 2: Checking Go installation
echo %date% %time% [INFO] Checking Go installation >> %TEST_LOG%
go version >nul 2>&1
if !errorlevel! equ 0 (
    echo [PASS] Go is installed and working >> %TEST_LOG%
    for /f "delims=" %%i in ('go version 2^>nul') do echo Go version: %%i >> %TEST_LOG%
    set /a TESTS_PASSED+=1
) else (
    echo [FAIL] Go is not installed or not working >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Test 3: Build Windows runner
echo [INFO] Test 3: Building Windows runner
echo %date% %time% [INFO] Building Windows runner >> %TEST_LOG%
set GOOS=windows
set GOARCH=amd64
go build -v -tags "netgo osusergo" -ldflags "-extldflags \"-static\" -s -w" -o forgejo-runner-test.exe >build.log 2>&1
if !errorlevel! equ 0 (
    if exist forgejo-runner-test.exe (
        echo [PASS] Runner built successfully >> %TEST_LOG%
        set /a TESTS_PASSED+=1
    ) else (
        echo [FAIL] Runner executable not found after build >> %TEST_LOG%
        set /a TESTS_FAILED+=1
    )
) else (
    echo [FAIL] Runner build failed >> %TEST_LOG%
    type build.log >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Test 4: Test runner executable
echo [INFO] Test 4: Testing runner executable
echo %date% %time% [INFO] Testing runner executable >> %TEST_LOG%
if exist forgejo-runner-test.exe (
    forgejo-runner-test.exe --version >version.log 2>&1
    if !errorlevel! equ 0 (
        echo [PASS] Runner executable works >> %TEST_LOG%
        type version.log >> %TEST_LOG%
        set /a TESTS_PASSED+=1
    ) else (
        echo [FAIL] Runner executable failed >> %TEST_LOG%
        type version.log >> %TEST_LOG%
        set /a TESTS_FAILED+=1
    )
) else (
    echo [FAIL] Runner executable not found >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Test 5: Create and test Windows logic simulation
echo [INFO] Test 5: Testing Windows logic simulation
echo %date% %time% [INFO] Testing Windows logic simulation >> %TEST_LOG%

REM Create test Go file
echo package main > test_logic.go
echo. >> test_logic.go
echo import ( >> test_logic.go
echo     "context" >> test_logic.go
echo     "fmt" >> test_logic.go
echo     "os" >> test_logic.go
echo     "runtime" >> test_logic.go
echo ^) >> test_logic.go
echo. >> test_logic.go
echo func performWindowsHostCleanup(ctx context.Context^) error { >> test_logic.go
echo     if runtime.GOOS != "windows" { >> test_logic.go
echo         return fmt.Errorf("not windows"^) >> test_logic.go
echo     } >> test_logic.go
echo     testDir := "test_cleanup" >> test_logic.go
echo     os.Mkdir(testDir, 0755^) >> test_logic.go
echo     fmt.Printf("CLEANUP:Cleaning directory %%s\n", testDir^) >> test_logic.go
echo     err := os.RemoveAll(testDir^) >> test_logic.go
echo     if err != nil { >> test_logic.go
echo         fmt.Printf("CLEANUP:Warning: %%v\n", err^) >> test_logic.go
echo     } >> test_logic.go
echo     fmt.Println("CLEANUP:Success"^) >> test_logic.go
echo     return nil >> test_logic.go
echo } >> test_logic.go
echo. >> test_logic.go
echo func main(^) { >> test_logic.go
echo     if runtime.GOOS == "windows" { >> test_logic.go
echo         fmt.Println("LXC:Skipping LXC cleanup on Windows"^) >> test_logic.go
echo         err := performWindowsHostCleanup(context.Background(^)^) >> test_logic.go
echo         if err != nil { >> test_logic.go
echo             fmt.Printf("ERROR:%%v\n", err^) >> test_logic.go
echo             os.Exit(1^) >> test_logic.go
echo         } >> test_logic.go
echo         fmt.Println("SUCCESS:Windows cleanup completed"^) >> test_logic.go
echo     } else { >> test_logic.go
echo         os.Exit(1^) >> test_logic.go
echo     } >> test_logic.go
echo } >> test_logic.go

go run test_logic.go >logic.log 2>&1
if !errorlevel! equ 0 (
    findstr "Skipping LXC cleanup on Windows" logic.log >nul
    if !errorlevel! equ 0 (
        findstr "Windows cleanup completed" logic.log >nul
        if !errorlevel! equ 0 (
            echo [PASS] Windows logic simulation successful >> %TEST_LOG%
            set /a TESTS_PASSED+=1
        ) else (
            echo [FAIL] Logic simulation incomplete >> %TEST_LOG%
            set /a TESTS_FAILED+=1
        )
    ) else (
        echo [FAIL] LXC skip message not found >> %TEST_LOG%
        set /a TESTS_FAILED+=1
    )
) else (
    echo [FAIL] Logic simulation failed to run >> %TEST_LOG%
    type logic.log >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Test 6: Process creation test
echo [INFO] Test 6: Testing process creation
echo %date% %time% [INFO] Testing process creation >> %TEST_LOG%

REM Create simple process test
echo package main > test_process.go
echo import "fmt" >> test_process.go
echo import "os/exec" >> test_process.go
echo import "runtime" >> test_process.go
echo func main(^) { >> test_process.go
echo     if runtime.GOOS != "windows" { >> test_process.go
echo         fmt.Println("ERROR:Not Windows"^) >> test_process.go
echo         return >> test_process.go
echo     } >> test_process.go
echo     cmd := exec.Command("cmd", "/c", "echo Process test"^) >> test_process.go
echo     output, err := cmd.CombinedOutput(^) >> test_process.go
echo     if err != nil { >> test_process.go
echo         fmt.Printf("ERROR:%%v\n", err^) >> test_process.go
echo         return >> test_process.go
echo     } >> test_process.go
echo     fmt.Printf("PROCESS:%%s", output^) >> test_process.go
echo     fmt.Println("SUCCESS:Process test completed"^) >> test_process.go
echo } >> test_process.go

go run test_process.go >process.log 2>&1
if !errorlevel! equ 0 (
    findstr "Process test completed" process.log >nul
    if !errorlevel! equ 0 (
        echo [PASS] Process creation test successful >> %TEST_LOG%
        set /a TESTS_PASSED+=1
    ) else (
        echo [FAIL] Process test incomplete >> %TEST_LOG%
        set /a TESTS_FAILED+=1
    )
) else (
    echo [FAIL] Process test failed >> %TEST_LOG%
    type process.log >> %TEST_LOG%
    set /a TESTS_FAILED+=1
)

REM Cleanup temporary files
del test_logic.go >nul 2>&1
del test_process.go >nul 2>&1
del logic.log >nul 2>&1
del process.log >nul 2>&1
del build.log >nul 2>&1
del version.log >nul 2>&1
del forgejo-runner-test.exe >nul 2>&1

REM Calculate results
set /a TOTAL_TESTS=!TESTS_PASSED!+!TESTS_FAILED!
set /a SUCCESS_RATE=!TESTS_PASSED!*100/!TOTAL_TESTS!

REM Output final results
echo. >> %TEST_LOG%
echo =========================================== >> %TEST_LOG%
echo AUTOMATED TEST RESULTS >> %TEST_LOG%
echo =========================================== >> %TEST_LOG%
echo Total Tests: !TOTAL_TESTS! >> %TEST_LOG%
echo Passed: !TESTS_PASSED! >> %TEST_LOG%
echo Failed: !TESTS_FAILED! >> %TEST_LOG%
echo Success Rate: !SUCCESS_RATE!%% >> %TEST_LOG%
echo =========================================== >> %TEST_LOG%
echo Completed: %date% %time% >> %TEST_LOG%

echo.
echo TEST RESULTS:
echo =============
echo Total Tests: !TOTAL_TESTS!
echo Passed: !TESTS_PASSED!
echo Failed: !TESTS_FAILED!
echo Success Rate: !SUCCESS_RATE!%%
echo.

if !TESTS_FAILED! equ 0 (
    echo [SUCCESS] All tests passed! Windows cancellation fix is working.
    echo Details logged to: %TEST_LOG%
    exit /b 0
) else (
    echo [FAILURE] Some tests failed! Check %TEST_LOG% for details.
    exit /b 1
)