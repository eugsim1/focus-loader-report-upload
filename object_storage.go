package main

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/oracle/oci-go-sdk/v65/common"
	"github.com/oracle/oci-go-sdk/v65/objectstorage"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

func listAllObjects(ctx context.Context, client objectstorage.ObjectStorageClient, namespaceName, bucketName, prefix, start string) ([]objectstorage.ObjectSummary, error) {
	var out []objectstorage.ObjectSummary
	currentStart := start

	for {
		resp, err := client.ListObjects(ctx, objectstorage.ListObjectsRequest{
			NamespaceName: common.String(namespaceName),
			BucketName:    common.String(bucketName),
			Prefix:        common.String(prefix),
			Start:         optionalString(currentStart),
			Fields:        common.String("timeCreated,size"),
		})
		if err != nil {
			return nil, fmt.Errorf("list objects: %w", err)
		}

		out = append(out, resp.Objects...)
		if resp.NextStartWith == nil || value(resp.NextStartWith) == "" {
			break
		}
		currentStart = value(resp.NextStartWith)
	}

	return out, nil
}

func processFocusFiles(
	ctx context.Context,
	db *sql.DB,
	objectClient objectstorage.ObjectStorageClient,
	objects []objectstorage.ObjectSummary,
	cmd commandLine,
	tenancy tenancyInfo,
	compartments []compartmentInfo,
	focusNamespaceName string,
	focusBucketName string,
	dbPass string,
	cfg appConfig,
	stateStore *processedStore,
	uploadStateStore *processedStore,
	uploadSession *transformedUploadSession,
) (int, error) {
	if len(objects) == 0 {
		return 0, nil
	}

	workerCount := cmd.workers
	if workerCount > len(objects) {
		workerCount = len(objects)
	}

	type queuedObject struct {
		index int
		file  objectstorage.ObjectSummary
	}

	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	jobs := make(chan queuedObject)
	errCh := make(chan error, workerCount)
	var wg sync.WaitGroup
	var loadedMu sync.Mutex
	loaded := 0

	for workerID := 1; workerID <= workerCount; workerID++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			for job := range jobs {
				select {
				case <-workerCtx.Done():
					return
				default:
				}

				result, err := loadFocusFile(
					workerCtx,
					db,
					objectClient,
					job.file,
					cmd,
					tenancy,
					compartments,
					job.index,
					len(objects),
					focusNamespaceName,
					focusBucketName,
					dbPass,
					cfg,
					workerID,
					uploadSession,
				)
				if err != nil {
					select {
					case errCh <- fmt.Errorf("worker %d failed on %s: %w", workerID, value(job.file.Name), err):
					default:
					}
					cancel()
					return
				}

				if result.Loaded {
					if err := stateStore.Mark(result); err != nil {
						select {
						case errCh <- fmt.Errorf("worker %d failed to checkpoint %s: %w", workerID, result.FileName, err):
						default:
						}
						cancel()
						return
					}

					loadedMu.Lock()
					loaded++
					loadedMu.Unlock()
				}
				if uploadStateStore != nil && result.FileName != "" {
					if err := uploadStateStore.MarkUploaded(result); err != nil {
						select {
						case errCh <- fmt.Errorf("worker %d failed to checkpoint uploaded file %s: %w", workerID, result.FileName, err):
						default:
						}
						cancel()
						return
					}
				}
			}
		}(workerID)
	}

sendJobs:
	for i, objectFile := range objects {
		select {
		case <-workerCtx.Done():
			break sendJobs
		case jobs <- queuedObject{index: i + 1, file: objectFile}:
		}
	}
	close(jobs)
	wg.Wait()

	select {
	case err := <-errCh:
		return loaded, err
	default:
		return loaded, nil
	}
}

func loadFocusFile(
	ctx context.Context,
	db *sql.DB,
	objectClient objectstorage.ObjectStorageClient,
	objectFile objectstorage.ObjectSummary,
	cmd commandLine,
	tenancy tenancyInfo,
	compartments []compartmentInfo,
	fileNum int,
	totalFiles int,
	focusNamespaceName string,
	focusBucketName string,
	dbPass string,
	cfg appConfig,
	workerID int,
	uploadSession *transformedUploadSession,
) (loadResult, error) {
	startTimeStr := currentDateTime()
	numRows := 0

	objectName := value(objectFile.Name)
	filename := filepath.Base(strings.ReplaceAll(objectName, "/", string(os.PathSeparator)))
	parts := strings.Split(objectName, "/")
	fileDate := ""
	if len(parts) > 3 {
		fileDate = strings.Join(parts[1:4], "-")
	}
	fileSizeMB := int64(math.Round(float64(valueInt64(objectFile.Size)) / 1024 / 1024))
	fileNameFull := objectName
	fileID := strings.TrimSuffix(filename, ".csv.gz")
	fileID = strings.TrimSuffix(fileID, ".gz")
	fileTime := ""
	if objectFile.TimeCreated != nil {
		fileTime = objectFile.TimeCreated.Time.Format("2006-01-02 15:04")
	}

	if cmd.fileNameFull != "" && fileNameFull != cmd.fileNameFull {
		return loadResult{}, nil
	}
	if cmd.fileDate != "" && fileDate < cmd.fileDate {
		return loadResult{}, nil
	}

	pathFilename := filepath.Join(workReportDir, safeWorkFileName(objectName))
	fmt.Printf("\n   Processing file '%s' - %d MB, #%d/%d\n", fileNameFull, fileSizeMB, fileNum, totalFiles)

	resp, err := objectClient.GetObject(ctx, objectstorage.GetObjectRequest{
		NamespaceName: common.String(focusNamespaceName),
		BucketName:    common.String(focusBucketName),
		ObjectName:    common.String(objectName),
	})
	if err != nil {
		return loadResult{}, fmt.Errorf("get object %s: %w", objectName, err)
	}
	defer resp.Content.Close()

	tmpPath := pathFilename + ".download"
	outFile, err := os.Create(tmpPath)
	if err != nil {
		return loadResult{}, fmt.Errorf("create %s: %w", tmpPath, err)
	}
	if _, err := io.Copy(outFile, resp.Content); err != nil {
		outFile.Close()
		return loadResult{}, fmt.Errorf("download %s: %w", objectName, err)
	}
	if err := outFile.Close(); err != nil {
		return loadResult{}, fmt.Errorf("close %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, pathFilename); err != nil {
		return loadResult{}, fmt.Errorf("rename downloaded file: %w", err)
	}

	csvFile := strings.TrimSuffix(pathFilename, ".gz")
	tagKeyValues, err := transformFocusCSV(ctx, db, cfg, pathFilename, csvFile, tenancy.Name, fileID, compartments, cmd, &numRows)
	if err != nil {
		return loadResult{}, err
	}

	fmt.Printf("   CSV created: %s (%d rows)\n", csvFile, numRows)
	if uploadSession != nil {
		if err := uploadSession.Upload(ctx, workerID, fileNameFull, csvFile, numRows); err != nil {
			return loadResult{}, err
		}
		if !cmd.loadAfterUpload {
			if cmd.keepWorkFiles {
				fmt.Printf("   Keeping transformed work files for '%s'\n", fileNameFull)
			} else {
				warnRemove(pathFilename)
				warnRemove(csvFile)
			}
			return loadResult{
				Loaded:     false,
				FileName:   fileNameFull,
				FileID:     fileID,
				NumRows:    numRows,
				FileSizeMB: fileSizeMB,
			}, nil
		}
	}

	tableName, err := cfg.table("FOCUS")
	if err != nil {
		return loadResult{}, err
	}

	generatedCtl := csvFile + ".ctl"
	ctlInfo, err := prepareCtlFile(cmd.ctlFile, generatedCtl, tableName, csvFile)
	if err != nil {
		return loadResult{}, err
	}

	loaderResult, loaderErr := runSQLLoader(cmd.dbUser, dbPass, cmd.dbName, ctlInfo, csvFile, csvFile+".log")
	if auditErr := insertSQLLoaderAudit(ctx, db, cfg, tenancy.Name, fileID, fileNameFull, loaderResult); auditErr != nil {
		if loaderErr != nil {
			return loadResult{}, fmt.Errorf("%w; additionally failed to insert SQL*Loader audit: %v", loaderErr, auditErr)
		}
		fmt.Printf("WARN: SQL*Loader audit insert failed after successful load: %v\n", auditErr)
	}
	if loaderErr != nil {
		return loadResult{}, loaderErr
	}

	if cmd.skipTagKeys {
		fmt.Printf("   Tag key/value metadata skipped: unique_values=%d\n", len(tagKeyValues))
	} else if len(tagKeyValues) > 0 {
		if err := insertTagKeyValues(ctx, db, cfg, tagKeyValues); err != nil {
			return loadResult{}, err
		}
	}

	if err := insertLoadStats(ctx, db, cfg, tenancy.Name, "FOCUS", fileID, fileNameFull, fileSizeMB, fileTime, numRows, startTimeStr, fileNum, totalFiles); err != nil {
		return loadResult{}, err
	}

	if cmd.keepWorkFiles {
		fmt.Printf("   Keeping work files for '%s'\n", fileNameFull)
	} else {
		warnRemove(pathFilename)
		warnRemove(csvFile)
		warnRemove(generatedCtl)
	}

	return loadResult{
		Loaded:     true,
		FileName:   fileNameFull,
		FileID:     fileID,
		NumRows:    numRows,
		FileSizeMB: fileSizeMB,
	}, nil
}
