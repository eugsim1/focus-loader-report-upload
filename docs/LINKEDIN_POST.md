# LinkedIn Post

🚀 **A fast, practical ETL utility for OCI FOCUS and FinOps reports**

I have been working on a multi-worker ETL utility written in Go to retrieve, transform, enrich, and deliver OCI FOCUS cost reports.

The utility supports two main scenarios:

- Download and enrich OCI FOCUS reports, then publish the transformed CSV files to another Object Storage bucket—without running SQL*Loader.
- Download and enrich the reports, then load them into Oracle Autonomous Database using SQL*Loader.

It is designed for both recent and historical data. You can define any starting date, process multiple files concurrently, and use persistent checkpoints for incremental execution.

Some of the implemented capabilities include:

✅ Configurable historical starting date
✅ Parallel file processing
✅ Incremental retrieval and restart checkpoints
✅ Cross-tenancy execution using OCI API-key configuration or instance principals
✅ Compartment hierarchy enrichment
✅ Extraction of up to four configurable tag values in the current release, with further extension possible
✅ Transformed-file delivery to Object Storage
✅ Autonomous Database loading through SQL*Loader
✅ Upload-result CSV files and SQL*Loader audit information
✅ Cron-ready execution with locking and persistent state
✅ Support for OCI Vault for database credentials

**Why SQL*Loader?**

SQL*Loader may be considered a mature technology, but maturity can be an advantage. It remains fast, predictable, well understood, and effective for loading large structured files into Oracle Database.

There are several valid ways to build a FinOps ingestion pipeline. Database-centric processing can be appropriate in some architectures. For this project, I preferred to perform file retrieval, decompression, enrichment, validation, and orchestration outside the database, while using SQL*Loader for the operation it performs particularly well: bulk data loading.

**Why Go?**

My first experiments used Python. Python remains an excellent option, but for this file-intensive and concurrent workload, Go provided:

- straightforward concurrency;
- good execution performance;
- simple deployment as a single binary;
- strong error handling;
- efficient streaming and CSV processing;
- convenient OCI SDK and database integration.

I have tested the utility across several tenancies by providing the appropriate OCI credentials, source configuration, and starting dates.

Codex was used as a development companion for code review, refactoring, testing, documentation, and improving consistency. The architecture and FinOps processing logic still require an understanding of the source data, Oracle Database, OCI permissions, error handling, and the expected target model.

AI-assisted development can be extremely useful, but it should complement—not replace—technical understanding and validation. Generating code is only one part of engineering a reliable data pipeline.

This work was inspired by the open-source [oracle-samples/usage-reports-to-adw](https://github.com/oracle-samples/usage-reports-to-adw) project, which uses the OCI Python SDK to extract cost reports and load them into Autonomous Database. I have used that project in several tenancies and appreciate the foundation and ideas it provided.

Repository: **https://github.com/eugsim1/focus-loader-report-upload**

⚠️ **Disclaimer**

This is a personal, experimental utility. It is not endorsed, certified, or supported by Oracle Corporation. It may contain defects and should initially be evaluated in a controlled test environment.

Review the source code, validate the results against OCI's official cost-management data, apply least-privilege permissions, back up checkpoints, and test carefully before considering production use.

Progress often requires experimentation—but experimentation should always be accompanied by validation, observability, and responsible risk management.

If you find the project interesting, a simple 👍 is greatly appreciated.

Thanks,
**Eugene**

#OCI #FinOps #FOCUS #OracleCloud #Golang #SQLLoader #AutonomousDatabase #CloudCostManagement #ETL #OpenSource
