package main

import (
	"compress/gzip"
	"context"
	"database/sql"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
)

func transformFocusCSV(ctx context.Context, db *sql.DB, cfg appConfig, gzipPath, csvPath, tenantName, fileID string, compartments []compartmentInfo, cmd commandLine, numRows *int) (map[tagKeyValue]struct{}, error) {
	in, err := os.Open(gzipPath)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", gzipPath, err)
	}
	defer in.Close()

	gz, err := gzip.NewReader(in)
	if err != nil {
		return nil, fmt.Errorf("open gzip %s: %w", gzipPath, err)
	}
	defer gz.Close()

	reader := csv.NewReader(gz)
	reader.FieldsPerRecord = -1

	header, err := reader.Read()
	if err != nil {
		return nil, fmt.Errorf("read csv header: %w", err)
	}

	index := map[string]int{}
	for i, h := range header {
		index[h] = i
	}

	out, err := os.Create(csvPath)
	if err != nil {
		return nil, fmt.Errorf("create %s: %w", csvPath, err)
	}
	defer out.Close()

	writer := csv.NewWriter(out)
	defer writer.Flush()

	compartmentPathByID := make(map[string]string, len(compartments))
	for _, c := range compartments {
		compartmentPathByID[c.ID] = c.Path
	}

	var tagRows []tagRow
	tagKeyValues := map[tagKeyValue]struct{}{}
	tagRowsTable := cfg.defaultTable("TEMP_OCI_FOCUS_TAGS")
	if configuredTable, ok := cfg.optionalTable("TAGS", "FOCUS_TAGS"); ok {
		tagRowsTable = configuredTable
	}
	tagRowsWithJSON := 0
	tagValuesParsed := 0
	tagRowsInserted := 0
	tagRowsSkipped := 0
	specialKeys := [4]string{
		strings.TrimSpace(cmd.tagSpecial1),
		strings.TrimSpace(cmd.tagSpecial2),
		strings.TrimSpace(cmd.tagSpecial3),
		strings.TrimSpace(cmd.tagSpecial4),
	}
	specialMatches := [4]int{}
	specialNonEmpty := [4]int{}
	flushTags := func() error {
		if len(tagRows) == 0 {
			return nil
		}
		rowsToInsert := len(tagRows)
		if cmd.skipTagRows {
			tagRowsSkipped += rowsToInsert
		} else {
			if err := insertTagRows(ctx, db, tagRowsTable, tagRows); err != nil {
				return err
			}
			tagRowsInserted += rowsToInsert
		}
		tagRows = tagRows[:0]
		return nil
	}

	start := time.Now()
	for {
		record, err := reader.Read()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read csv row: %w", err)
		}

		row := func(column string) string {
			if i, ok := index[column]; ok && i < len(record) {
				return record[i]
			}
			return ""
		}

		compartmentPath := compartmentPathByID[row("oci_CompartmentId")]
		tagSpecial1, tagSpecial2, tagSpecial3, tagSpecial4 := "", "", "", ""
		tagsData := ""

		if rawTags := row("Tags"); rawTags != "" {
			tagRowsWithJSON++
			if cmd.skipTags {
				tagsData = rawTags
			} else {
				var tags map[string]any
				if err := json.Unmarshal([]byte(rawTags), &tags); err != nil {
					fmt.Printf("Error parsing tags: %v\n", err)
				} else {
					b, err := json.Marshal(tags)
					if err != nil {
						fmt.Printf("Error serializing tags: %v\n", err)
					} else {
						tagsData = string(b)
					}

					chargePeriodStart := ""
					if cps := row("ChargePeriodStart"); cps != "" {
						chargePeriodStart = strings.ReplaceAll(substr(cps, 0, 19), "T", " ")
					}

					for key, val := range tags {
						tagKey := strings.TrimSpace(key)
						strValue := fmt.Sprint(val)
						if val != nil && strings.TrimSpace(strValue) != "" {
							tagValuesParsed++
							if cmd.skipTagRows {
								tagRowsSkipped++
							} else {
								tagRows = append(tagRows, tagRow{
									TenantName:        tenantName,
									ResourceID:        row("ResourceId"),
									ChargePeriodStart: chargePeriodStart,
									TagKey:            tagKey,
									TagValue:          strValue,
								})
							}
							tagKeyValues[tagKeyValue{
								TenantName: tenantName,
								TagKey:     tagKey,
								TagValue:   strValue,
							}] = struct{}{}
						}

						cleanSpecial := substr(strings.ReplaceAll(strings.TrimSpace(strValue), "oracleidentitycloudservice/", ""), 0, 4000)
						if specialKeys[0] != "" && tagKey == specialKeys[0] {
							specialMatches[0]++
							if cleanSpecial != "" {
								specialNonEmpty[0]++
							}
							tagSpecial1 = cleanSpecial
						}
						if specialKeys[1] != "" && tagKey == specialKeys[1] {
							specialMatches[1]++
							if cleanSpecial != "" {
								specialNonEmpty[1]++
							}
							tagSpecial2 = cleanSpecial
						}
						if specialKeys[2] != "" && tagKey == specialKeys[2] {
							specialMatches[2]++
							if cleanSpecial != "" {
								specialNonEmpty[2]++
							}
							tagSpecial3 = cleanSpecial
						}
						if specialKeys[3] != "" && tagKey == specialKeys[3] {
							specialMatches[3]++
							if cleanSpecial != "" {
								specialNonEmpty[3]++
							}
							tagSpecial4 = cleanSpecial
						}
					}
				}
			}
		}

		if len(tagRows) >= tagBatchSize {
			if err := flushTags(); err != nil {
				return nil, err
			}
		}

		outRow := []string{
			tenantName,
			fileID,
			row("BillingAccountId"),
			row("BillingAccountName"),
			row("BillingAccountType"),
			row("SubAccountId"),
			row("SubAccountName"),
			row("SubAccountType"),
			row("InvoiceId"),
			row("InvoiceIssuer"),
			row("Provider"),
			row("Publisher"),
			row("PricingCategory"),
			row("PricingCurrencyContractedUnitPrice"),
			row("PricingCurrencyEffectiveCost"),
			row("PricingCurrencyListUnitPrice"),
			row("PricingQuantity"),
			row("PricingUnit"),
			substr(row("BillingPeriodStart"), 0, 10),
			substr(row("BillingPeriodEnd"), 0, 10),
			substr(row("ChargePeriodStart"), 0, 16),
			substr(row("ChargePeriodEnd"), 0, 16),
			row("BilledCost"),
			row("BillingCurrency"),
			row("ConsumedQuantity"),
			row("ConsumedUnit"),
			row("ContractedCost"),
			row("ContractedUnitPrice"),
			row("EffectiveCost"),
			row("ListCost"),
			row("ListUnitPrice"),
			row("AvailabilityZone"),
			row("Region"),
			row("RegionName"),
			row("ResourceId"),
			row("ResourceName"),
			row("ResourceType"),
			tagsData,
			row("ServiceCategory"),
			row("ServiceSubCategory"),
			row("ServiceName"),
			row("CapacityReservationId"),
			row("CapacityReservationStatus"),
			row("ChargeCategory"),
			row("ChargeClass"),
			row("ChargeDescription"),
			row("ChargeFrequency"),
			row("CommitmentDiscountCategory"),
			row("CommitmentDiscountId"),
			row("CommitmentDiscountName"),
			row("CommitmentDiscountQuantity"),
			row("CommitmentDiscountStatus"),
			row("CommitmentDiscountType"),
			row("CommitmentDiscountUnit"),
			row("SkuId"),
			row("SkuPriceId"),
			row("SkuPriceDetails"),
			row("SkuMeter"),
			row("UsageQuantity"),
			row("UsageUnit"),
			row("oci_ReferenceNumber"),
			row("oci_CompartmentId"),
			row("oci_CompartmentName"),
			compartmentPath,
			row("oci_OverageFlag"),
			row("oci_UnitPriceOverage"),
			row("oci_BilledQuantityOverage"),
			row("oci_CostOverage"),
			row("oci_AttributedUsage"),
			row("oci_AttributedCost"),
			row("oci_BackReferenceNumber"),
			tagSpecial1,
			tagSpecial2,
			tagSpecial3,
			tagSpecial4,
		}

		if err := writer.Write(outRow); err != nil {
			return nil, fmt.Errorf("write transformed csv row: %w", err)
		}
		(*numRows)++
	}

	if err := flushTags(); err != nil {
		return nil, err
	}
	writer.Flush()
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("flush transformed csv: %w", err)
	}

	fmt.Printf("   CSV transform time: %.2f seconds\n", time.Since(start).Seconds())
	fmt.Printf("   Tags parsed: rows_with_tags=%d values=%d unique_key_values=%d inserted_rows=%d skipped_rows=%d skip_tags=%t skip_tag_rows=%t table=%s\n",
		tagRowsWithJSON, tagValuesParsed, len(tagKeyValues), tagRowsInserted, tagRowsSkipped, cmd.skipTags, cmd.skipTagRows, tagRowsTable)
	printTagSpecialStats(specialKeys, specialMatches, specialNonEmpty, cmd.skipTags)
	return tagKeyValues, nil
}

func printTagSpecialStats(keys [4]string, matches [4]int, nonEmpty [4]int, skipTags bool) {
	anyConfigured := false
	for _, key := range keys {
		if key != "" {
			anyConfigured = true
			break
		}
	}
	if !anyConfigured {
		fmt.Println("   Tag special keys: none configured; TAG_SPECIAL1..4 will be NULL")
		return
	}
	if skipTags {
		fmt.Println("   Tag special keys: skipped because -skip-tags is enabled; TAG_SPECIAL1..4 will be NULL")
		return
	}

	for i, key := range keys {
		if key == "" {
			continue
		}
		fmt.Printf("   Tag special ts%d=%q matches=%d non_empty_values=%d\n", i+1, key, matches[i], nonEmpty[i])
		if matches[i] == 0 {
			fmt.Printf("   WARN: ts%d key %q was not found in this file's Tags JSON\n", i+1, key)
		} else if nonEmpty[i] == 0 {
			fmt.Printf("   WARN: ts%d key %q was found, but all values were empty; Oracle stores empty strings as NULL\n", i+1, key)
		}
	}
}
