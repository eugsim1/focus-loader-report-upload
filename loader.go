package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func prepareCtlFile(templateCtl, outputCtl, tableName, csvFile string) (ctlFileInfo, error) {
	content, err := os.ReadFile(templateCtl)
	if err != nil {
		return ctlFileInfo{}, fmt.Errorf("read control file %s: %w", templateCtl, err)
	}

	updated := strings.ReplaceAll(string(content), "{TABLE_NAME}", tableName)
	updated = strings.ReplaceAll(updated, "{csv_file}", csvFile)
	updated = strings.ReplaceAll(updated, "{CSV_FILE}", csvFile)

	if unresolved := unresolvedCtlPlaceholders(updated); len(unresolved) > 0 {
		return ctlFileInfo{}, fmt.Errorf("generated control file would still contain unresolved placeholder(s): %s", strings.Join(unresolved, ", "))
	}

	if err := os.WriteFile(outputCtl, []byte(updated), 0o644); err != nil {
		return ctlFileInfo{}, fmt.Errorf("write generated control file %s: %w", outputCtl, err)
	}

	upper := strings.ToUpper(updated)
	info := ctlFileInfo{
		Path:           outputCtl,
		HasInfile:      strings.Contains(upper, "INFILE"),
		HasBadfile:     strings.Contains(upper, "BADFILE"),
		HasDiscardfile: strings.Contains(upper, "DISCARDFILE"),
		HasDirect:      strings.Contains(upper, "DIRECT="),
		HasErrors:      strings.Contains(upper, "ERRORS="),
	}

	fmt.Printf("   Generated SQL*Loader control file: %s\n", outputCtl)
	fmt.Printf("   SQL*Loader data file in control : %s\n", csvFile)
	return info, nil
}

func unresolvedCtlPlaceholders(content string) []string {
	var unresolved []string
	for _, token := range []string{"{TABLE_NAME}", "{csv_file}", "{CSV_FILE}"} {
		if strings.Contains(content, token) {
			unresolved = append(unresolved, token)
		}
	}
	return unresolved
}

func runSQLLoader(user, password, dsn string, ctlInfo ctlFileInfo, dataFile, logFile string) (sqlLoaderResult, error) {
	result := sqlLoaderResult{
		DataFile:  dataFile,
		LogFile:   logFile,
		Status:    "STARTED",
		StartedAt: time.Now(),
	}

	fileInfo, err := os.Stat(dataFile)
	if err != nil {
		result.Status = "FAILED"
		result.EndedAt = time.Now()
		result.DurationSeconds = result.EndedAt.Sub(result.StartedAt).Seconds()
		result.ErrorMessage = err.Error()
		return result, fmt.Errorf("stat SQL*Loader data file %s: %w", dataFile, err)
	}
	result.FileSizeBytes = fileInfo.Size()

	totalLines, err := countFileLines(dataFile)
	if err != nil {
		result.Status = "FAILED"
		result.EndedAt = time.Now()
		result.DurationSeconds = result.EndedAt.Sub(result.StartedAt).Seconds()
		result.ErrorMessage = err.Error()
		return result, fmt.Errorf("count SQL*Loader data file lines %s: %w", dataFile, err)
	}
	result.TotalFileLines = totalLines

	args := []string{
		fmt.Sprintf("%s/%s@%s", user, password, dsn),
		"control=" + ctlInfo.Path,
		"log=" + logFile,
	}
	if !ctlInfo.HasInfile {
		args = append(args, "data="+dataFile)
	}
	if !ctlInfo.HasBadfile {
		args = append(args, "bad="+dataFile+".bad")
	}
	if !ctlInfo.HasDiscardfile {
		args = append(args, "discard="+dataFile+".dsc")
	}
	if !ctlInfo.HasDirect {
		args = append(args, "direct=true")
	}
	if !ctlInfo.HasErrors {
		args = append(args, "errors=100000")
	}

	maskedArgs := append([]string{}, args...)
	maskedArgs[0] = fmt.Sprintf("%s/xxxxxxx@%s", user, dsn)
	fmt.Printf("Running SQL*Loader: sqlldr %s\n", quoteArgsForLog(maskedArgs))
	if password == "" {
		fmt.Println("SQL*Loader password: not supplied")
	} else {
		fmt.Printf("SQL*Loader password: supplied (%d characters, masked)\n", len(password))
	}

	cmd := exec.Command("sqlldr", args...)
	output, err := cmd.CombinedOutput()
	result.EndedAt = time.Now()
	result.DurationSeconds = result.EndedAt.Sub(result.StartedAt).Seconds()

	parseSQLLoaderResult(&result, string(output), logFile)
	if err != nil {
		result.Status = "FAILED"
		result.ErrorMessage = substr(fmt.Sprintf("%v\n%s", err, string(output)), 0, 4000)
		return result, fmt.Errorf("SQL*Loader failed: %w\n%s", err, string(output))
	}

	result.Status = "SUCCESS"
	fmt.Println("SQL*Loader completed successfully")
	return result, nil
}

func quoteArgsForLog(args []string) string {
	quoted := make([]string, len(args))
	for i, arg := range args {
		quoted[i] = fmt.Sprintf("%q", arg)
	}
	return strings.Join(quoted, " ")
}

func countFileLines(path string) (int, error) {
	file, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer file.Close()

	buf := make([]byte, 1024*1024)
	newline := []byte{'\n'}
	lines := 0
	readAny := false
	var lastByte byte

	for {
		n, readErr := file.Read(buf)
		if n > 0 {
			readAny = true
			lines += bytes.Count(buf[:n], newline)
			lastByte = buf[n-1]
		}
		if readErr == nil {
			continue
		}
		if readErr == io.EOF {
			break
		}
		return 0, readErr
	}

	if readAny && lastByte != '\n' {
		lines++
	}
	return lines, nil
}

func parseSQLLoaderResult(result *sqlLoaderResult, commandOutput, logFile string) {
	text := commandOutput
	if logBytes, err := os.ReadFile(logFile); err == nil {
		text += "\n" + string(logBytes)
	}

	result.RowsInserted = firstSQLLoaderInt(text, `(?mi)^\s*(\d[\d,]*)\s+Rows successfully loaded\.`)
	result.RowsRejected = firstSQLLoaderInt(text, `(?mi)Total logical records rejected:\s*(\d[\d,]*)`)
	result.RowsDiscarded = firstSQLLoaderInt(text, `(?mi)Total logical records discarded:\s*(\d[\d,]*)`)
	result.RowsSkipped = firstSQLLoaderInt(text, `(?mi)Total logical records skipped:\s*(\d[\d,]*)`)
	result.TotalRowsRead = firstSQLLoaderInt(text, `(?mi)Total logical records read:\s*(\d[\d,]*)`)

	failedByDataErrors := firstSQLLoaderInt(text, `(?mi)^\s*(\d[\d,]*)\s+Rows not loaded due to data errors\.`)
	failedByWhenClause := firstSQLLoaderInt(text, `(?mi)^\s*(\d[\d,]*)\s+Rows not loaded because all WHEN clauses were failed\.`)
	failedByNullFields := firstSQLLoaderInt(text, `(?mi)^\s*(\d[\d,]*)\s+Rows not loaded because all fields were null\.`)
	result.RowsFailed = failedByDataErrors + failedByWhenClause + failedByNullFields
	if result.RowsFailed == 0 {
		result.RowsFailed = result.RowsRejected + result.RowsDiscarded
	}
}

func firstSQLLoaderInt(text, pattern string) int {
	match := regexp.MustCompile(pattern).FindStringSubmatch(text)
	if len(match) < 2 {
		return 0
	}
	value, err := strconv.Atoi(strings.ReplaceAll(match[1], ",", ""))
	if err != nil {
		return 0
	}
	return value
}
