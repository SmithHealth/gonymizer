package gonymizer

import (
	"bytes"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
)

func TestCreateDatabase(t *testing.T) {
	conf := GetTestDbConf(TestDb)
	require.Nil(t, CreateDatabase(conf))

	// Setup for Failure! Mwhaha
	conf = PGConfig{}
	conf.Host = "somewhereinoutterspace.dune"
	conf.DefaultDBName = "Arrakis"
	conf.SSLMode = "enable"

	require.NotNil(t, CreateDatabase(conf))
}

func TestDropDatabase(t *testing.T) {
	conf := GetTestDbConf(TestDb)

	// We need to make sure no one is connected to the database before dropping
	psqlDbConf := GetTestDbConf(TestDb)
	psqlDbConf.DefaultDBName = "postgres"
	psqlConn, err := OpenDB(psqlDbConf)
	require.Nil(t, err)
	err = KillDatabaseConnections(psqlConn, conf.DefaultDBName)
	if err != nil && err.Error() != "sql: no rows in result set" {
		require.Nil(t, err)
	}

	// Now drop the database
	require.Nil(t, DropDatabase(conf))
}

func TestSQLCommandFileFunc(t *testing.T) {
	conf := GetTestDbConf(TestDb)
	require.NotNil(t, SQLCommandFile(conf, "asdf", false))
	require.Nil(t, SQLCommandFile(conf, TestSQLCommandFile, true))
}

func TestPsqlCommand(t *testing.T) {
	var (
		errBuffer bytes.Buffer
		outBuffer bytes.Buffer
	)

	conf := GetTestDbConf(TestDb)
	dburl := conf.BaseURI()
	cmd := "psql"
	args := []string{
		dburl,
		"-c", // run a command
		` SELECT table_catalog, table_schema, table_name, column_name, data_type, ordinal_position,
			CASE
			    WHEN is_nullable = 'YES' THEN
			        TRUE
          WHEN is_nullable = 'NO' THEN
              FALSE
					END AS is_nullable
			FROM information_schema.columns
			WHERE table_schema = 'public'
			ORDER BY table_name, ordinal_position;`,
	}
	require.Nil(t, ExecPostgresCmd(cmd, args...))
	require.Nil(t, ExecPostgresCommandOutErr(&outBuffer, &errBuffer, cmd, args...))

	viper.Set("PG_BIN_DIR", "/tmp")
	require.NotNil(t, ExecPostgresCmd(cmd, args...))
	viper.Set("PG_BIN_DIR", nil)
}
