package gonymizer

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"github.com/spf13/viper"
	"io"
	mathRand "math/rand"
	"os"
	"strings"
	"unicode"

	log "github.com/sirupsen/logrus"
)

var lineCount = int64(0) // Used to notify user progress during processing

// StateChangeTokenBeginCopy is the token used to notify the processor that we have hit SQL-COPY in the dump file
// StateChangeTokenEndCopy is the token used to notify the processor that we are done with SQL-COPY
const (
	StateChangeTokenBeginCopy = "COPY"
	StateChangeTokenEndCopy   = "\\."
)

// LineState contains all the required information for parsing a line in the SQL dump file.
type LineState struct {
	LineNum     int64
	IsRow       bool
	SchemaName  string
	TableName   string
	ColumnNames []string
}

// Clear will clear out all known line stat for the current LineState object.
func (curLine *LineState) Clear() {
	curLine.IsRow = false
	curLine.SchemaName = ""
	curLine.TableName = ""
	curLine.ColumnNames = nil
}

// CreateDumpFile will create a PostgreSQL dump file from the specified PGConfig to the location, and with
// restrictions, that are provided by the inputs to the function.
func CreateDumpFile(
	conf PGConfig,
	dumpfilePath,
	schemaPrefix string,
	excludeTables,
	excludeDataTables,
	excludeCreateSchemas,
	schemas []string,
	skipProcedures bool,
) error {

	cmd := "pg_dump"
	args := []string{
		conf.URI(),
		"--oids",
		"--no-owner",
	}

	var errBuffer bytes.Buffer

	log.Info("Dumping the following schemas: ", schemas)

	if len(schemas) < 1 {
		args = append(args, fmt.Sprintf("--schema=public"))
		schemas = append(schemas, "public")
	} else {
		// Add all schemas that match schemaPrefix to the dump list
		for _, schema := range schemas {
			if strings.HasPrefix(schemaPrefix, schema) {
				args = append(args, fmt.Sprintf("--schema=%s*.*", schemaPrefix))
			} else {
				args = append(args, fmt.Sprintf("--schema=%s.*", schema))
			}
		}
	}

	// Exclude system schemas
	for _, sch := range excludeCreateSchemas {
		args = append(args, fmt.Sprintf("--exclude-table=%s.*", sch))
	}

	// Exclude tables that are not needed (schema will not be dumped)
	for _, tbl := range excludeTables {
		// According to: https://www.postgresql.org/docs/9.3/static/app-pgdump.html we need to add a flag for every table
		// unless we use a regex match which we do not want in this case. Make sure to read the NOTES under --table. They
		// apply here as well.

		// tbl format => "schema_name.table_name"
		args = append(args, fmt.Sprintf("--exclude-table=%s", tbl))
	}

	// Exclude tables that we do not need data from (but keep the schema... restores a blank table)
	for _, tbl := range excludeDataTables {
		args = append(args, fmt.Sprintf("--exclude-table-data=%s", tbl))
	}

	outputFile, err := os.OpenFile(dumpfilePath, os.O_RDWR|os.O_CREATE, 0660)
	if err != nil {
		log.Debug("error open or create: ", err)
		log.Debug("outputFileName: ", dumpfilePath)
		return err
	}
	defer outputFile.Close()
	/*
	// Prepopulate our dumpfile with user defined functions
	if !skipProcedures {
		log.Info("Adding CREATE statements for: stored procedures")
		for _, schema := range schemas {
			var procedures, err = GetAllProceduresInSchema(conf, schema)
			if err != nil {
				return err
			}
			for _, proc := range procedures {
				// When pulling procedures from the information schema we are not given a ';' at the end of each
				// statement. Add the ';' here. Some functions will fail on import, but we ignore them since they are
				// system functions that are already defined in the 'public' schema
				_, err = outputFile.WriteString(fmt.Sprintf("%s;\n", proc))
				if err != nil {
					return err
				}
			}
		}
	}
	*/
	// Add create schema statements to top of dump file
	//log.Info("Adding CREATE statements for: non-system schemas")
	//if err := generateSchemaSQL(conf, outputFile, excludeCreateSchemas); err != nil {
	//		return err
	//	}

	// Now grab tables and data
	err = ExecPostgresCommandOutErr(outputFile, &errBuffer, cmd, args...)
	if err != nil {
		log.Error(err)
		log.Debug("stdErr: ", errBuffer.String())
		return err
	}

	return nil
}

// ProcessDumpFile will process the supplied dump file according to the supplied database map file. GenerateSeed can
// also be set to true which will inform the function to use Go's built-in random number generator.
func ProcessDumpFile(mapper *DBMapper, src, dst, postProcessFile string, generateSeed bool) error {
	var (
		inputLine  string
		outputLine string
	)
	if generateSeed {
		for {
			randVal, err := generateRandomInt64()
			if err != nil {
				log.Error(err)
			} else {
				log.Debugf("Using internal number generator for seed value: %d", randVal)
				mathRand.Seed(randVal)
				break
			}
		}
	} else {
		randVal := mapper.Seed
		if randVal == 0 {
			return errors.New("Expected non-zero Seed")
		}
		log.Debugf("Using map file for seed value: %d", randVal)
		mathRand.Seed(mapper.Seed)
	}

	srcFile, err := os.Open(src)
	if err != nil {
		log.Error(err)
		log.Debug("src: ", src)
		log.Debug("dst: ", dst)
		return err
	}
	defer srcFile.Close()

	fileReader := bufio.NewReader(srcFile)

	dstFile, err := os.Create(dst)
	if err != nil {
		log.Error(err)
		log.Debug("src: ", src)
		log.Debug("dst: ", dst)
		return err
	}
	defer dstFile.Close()

	// Call the preprocessor to write any required configuration settings to the top of the processed dump file
	err = preProcess(dstFile)
	if err != nil {
		log.Error("Unable to run preProcessor")
		return err
	}

	allDone := false
	state := new(LineState)

	for {
		lineCount++
		state.LineNum = lineCount
		inputLine, err = fileReader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				// readline will fail if it doesn't encounter our delimiter (\n)
				// EOF isn't a real error tho...

				// do nothing
				allDone = true
			} else {
				log.Error(err)
				log.Debug("src: ", src)
				log.Debug("dst: ", dst)
				log.Debug("lineCount: ", lineCount)
				log.Debug("inputLine: ", inputLine)
				return err
			}
		}

		state, outputLine, err = processLine(mapper, state, inputLine)

		if err != nil {
			log.Error("processLine failure: ", err)
			log.Debug("src: ", src)
			log.Debug("dst: ", dst)
			log.Debug("lineCount", lineCount)
			log.Debug("inputLine", inputLine)
			log.Debug("outputLine", outputLine)
			return err
		}

		bytesWritten, err := dstFile.WriteString(outputLine)
		if err != nil {
			log.Error(err)
			log.Debug("src: ", src)
			log.Debug("dst: ", dst)
			log.Debug("lineCount", lineCount)
			log.Debug("inputLine", inputLine)
			log.Debug("bytesWritten", bytesWritten)
			return err
		}

		if allDone {
			break
		}

		if lineCount%100000 == 0 {
			log.Info("Processing line number: ", lineCount)
		}
	}
	if strings.ToLower(viper.GetString("log-level")) == "debug" {
		err = writeDebugMap()
		if err != nil {
			return err
		}
	}
	// Add in SQL at the end of the dump file
	if len(postProcessFile) > 0 {
		err = postProcess(dstFile, postProcessFile)
		if err != nil {
			return err
		}
	}
	return nil
}

// generateRandomInt64 will generate a pseudo random 64bit integer which is used for seeding the Go random
// number generator.
func generateRandomInt64() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return int64(binary.LittleEndian.Uint64(b[:])), nil
}

// generateSchemaSQL will generate all needed CREATE SCHEMA statements that are needed for the processed dump file.
func generateSchemaSQL(conf PGConfig, outputFile *os.File, excludeCreateSchemas []string) error {
	var sql string

	// Prepopulate our dumpfile with drop / create schema statements
	otherSchemas, err := GetSchemasInDatabase(conf, excludeCreateSchemas)
	log.Info(otherSchemas)
	if err != nil {
		return err
	}
	for _, schema := range otherSchemas {
		if conf.Username != "" {
			sql = fmt.Sprintf(
				"CREATE SCHEMA IF NOT EXISTS %[1]s AUTHORIZATION %[2]s;\n"+
					"GRANT USAGE ON SCHEMA %[1]s TO %[2]s;\n"+
					"GRANT ALL PRIVILEGES ON SCHEMA %[1]s TO %[2]s;\n"+
					"GRANT ALL PRIVILEGES ON ALL TABLES IN SCHEMA %[1]s TO %[2]s;\n\n"+
					"ALTER DEFAULT PRIVILEGES IN SCHEMA %[1]s GRANT ALL ON TABLES TO %[2]s;\n\n", schema, conf.Username)
		} else {
			sql = fmt.Sprintf("CREATE SCHEMA IF NOT EXISTS %s;\n", schema)
		}
		_, err = outputFile.WriteString(sql)
		if err != nil {
			return err
		}
	}
	return nil
}

// processLine will process the current line in the dump file by deciding which state the processor should be in
// based on reading in the content of the current line in the dump file and analyzing it.
func processLine(mapper *DBMapper, state *LineState, inputLine string) (*LineState, string, error) {

	outputLine := inputLine
	trimmedInput := strings.TrimLeftFunc(inputLine, unicode.IsSpace)
	if len(trimmedInput) == 0 {
		return state, outputLine, nil
	}

	if strings.HasPrefix(trimmedInput, "--") {
		return state, outputLine, nil
	}

	if strings.HasPrefix(trimmedInput, StateChangeTokenBeginCopy) {
		state.parseCopyLine(inputLine)
		return state, outputLine, nil
	}

	if strings.HasPrefix(trimmedInput, StateChangeTokenEndCopy) {
		state.Clear()
		return state, outputLine, nil
	}

	if state.IsRow {
		return processRow(mapper, state, inputLine)
	}

	return state, outputLine, nil
}

// processRow will process the line in the dump file IFF it is a SQL-line (eventual row in the database after import).
func processRow(mapper *DBMapper, state *LineState, inputLine string) (*LineState, string, error) {

	rowVals := strings.Split(inputLine, "\t")
	outputVals := make([]string, 0, len(rowVals))

	for i, columnName := range state.ColumnNames {
		var (
			err        error
			escapeChar string
			output     string
		)

		cmap := mapper.ColumnMapper(state.SchemaName, state.TableName, columnName)
		val := rowVals[i]

		// Check to see if the column has an escape char at the end of it.
		// If so cut it and keep it for later
		if strings.HasSuffix(val, "\n") {
			escapeChar = "\n"
			val = strings.Replace(val, "\n", "", -1)
		} else if strings.HasSuffix(val, "\t") {
			escapeChar = "\t"
			val = strings.Replace(val, "\t", "", -1)
		}

		// If column value is nil or if this column is not mapped, keep the value and continue on
		if val == "\\N" || cmap == nil {
			output = val
		} else {
			output, err = processValue(cmap, val)
			if err != nil {
				log.Error(err)
				log.Debug("i: ", i)
				log.Debug("columnName: ", columnName)
				return state, "****************** PROCESS ROW ERROR ******************", err
			}
		}
		// Add escape character back to column
		output += escapeChar

		// Append the column to our new line
		outputVals = append(outputVals, output)
	}

	outputLine := strings.Join(outputVals, "\t")

	return state, outputLine, nil
}

// processValue will anonymize or ignore the current value for a given column in the dump file
func processValue(cmap *ColumnMapper, input string) (string, error) {
	var err error

	output := input

	for i, procDef := range cmap.Processors {

		pfunc := ProcessorCatalog[procDef.Name]

		if pfunc == nil {
			log.Error(err)
			log.Error("Unknown Processor Name: ", procDef.Name)
			log.Debug("i: ", i)
			log.Debug("procDef: ", procDef)
			log.Debug("cmap: ", cmap)
			log.Debug("input: ", input)
			return "", err

		}

		output, err = pfunc(cmap, input)
		if err != nil {
			log.Error(err)
			log.Debug("i: ", i)
			log.Debug("cmap: ", cmap)
			log.Debug("input: ", input)
			return "", err
		}
	}
	return output, nil
}

// parseCopyLine will parse the /copy line in a PostgreSQL dump file
func (curLine *LineState) parseCopyLine(inputLine string) {

	spaceSplts := strings.Split(inputLine, " ")
	schemaTableSplt := strings.Split(spaceSplts[1], ".")

	curLine.IsRow = true
	curLine.SchemaName = schemaTableSplt[0]
	curLine.TableName = schemaTableSplt[1]

	openSplts := strings.Split(inputLine, "(")
	parensContent := openSplts[1]
	closeSplits := strings.Split(parensContent, ")")
	parensContent = closeSplits[0]
	curLine.ColumnNames = strings.Split(parensContent, ",")

	for i, v := range curLine.ColumnNames {
		curLine.ColumnNames[i] = strings.TrimSpace(v)
	}

	debugLine := fmt.Sprintf(`
====================================================================================================================
 Schema.Table: %s.%s
 Line number:  %d
 Is a row:     %t
 Columns:      %s
====================================================================================================================`,
		curLine.SchemaName, curLine.TableName, curLine.LineNum, curLine.IsRow, strings.Join(curLine.ColumnNames, ", "))
	log.Debug(debugLine)
}

// preProcess writes data to the top of the processed file prior to anonymizing the dump file.
func preProcess(dstFile *os.File) (err error) {
	settings := `
--
-- Begin settings set by Gonymizer (pre-processor)
--

SET session_replication_role = replica;   /* Used to import without getting FK check errors   */
CREATE EXTENSION btree_gist;              /* BTree_Gist plugin required by some tables        */

--
-- End settings set by Gonymizer (pre-processor)
--
`
	_, err = dstFile.WriteString(settings)
	return err
}

// postProcess writes data to the end of the processed file after anonymization of the dump file is complete
func postProcess(dstFile *os.File, postProcessPath string) error {
	_, err := dstFile.WriteString(`
--
-- Begin settings set by Gonymizer (post-processor)
--
`)
	if err != nil {
		return err
	}
	postProcessFile, err := os.Open(postProcessPath)
	if err != nil {
		return err
	}
	defer postProcessFile.Close()

	postFile := bufio.NewReader(postProcessFile)

	for {
		inputLine, err := postFile.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				break
			} else {
				return err
			}
		}
		// Copy data from postProcessFile into processed dump file
		_, err = dstFile.WriteString(inputLine)
		if err != nil {
			return nil
		}
	}
	_, err = dstFile.WriteString(`
--
-- End settings set by Gonymizer (post-processor)
--
`)
	if err != nil {
		return err
	}

	return nil
}

// writeDebugMap is used to store the reverse of the original data to the anonymized data.
// WARNING: this is disabled by default and the programmer must add this function back in to use it. Only use this
// function when debugging improvements to the map and process commands.
func writeDebugMap() (err error) {
	// Dump map to disk for debug
	outputFile, err := os.OpenFile("/tmp/map.txt", os.O_RDWR|os.O_CREATE, 0660)
	if err != nil {
		log.Debug("outputFileName: /tmp/map.txt")
		return err
	}
	defer outputFile.Close()

	for k, v := range UUIDMap {
		_, err = outputFile.WriteString(fmt.Sprintf("%s => %s\n", k, v))
		if err != nil {
			return err
		}
	}
	for k1, v1 := range AlphaNumericMap {
		_, err = outputFile.WriteString(fmt.Sprintf("\n=================\n%s\n=================\n", k1))
		if err != nil {
			return err
		}
		for k2, v2 := range v1 {
			_, err = outputFile.WriteString(fmt.Sprintf("%s|\t%s => %s\n", k1, k2, v2))
			if err != nil {
				return err
			}
		}
	}
	return err
}
