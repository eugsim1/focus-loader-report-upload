package main

import (
	"bytes"
	"database/sql"

	_ "github.com/godror/godror"
	"github.com/godror/godror/dsn"
)

func databaseDriverDescription() string {
	return "godror (database/sql driver name: godror)"
}

func openOracleDB(cmd commandLine, password string) (*sql.DB, error) {
	var params bytes.Buffer
	_ = dsn.AppendLogfmt(&params, "user", cmd.dbUser)
	_ = dsn.AppendLogfmt(&params, "password", password)
	_ = dsn.AppendLogfmt(&params, "connectString", cmd.dbName)

	return sql.Open("godror", params.String())
}
