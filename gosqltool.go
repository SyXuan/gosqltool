package gosqltool

import (
	"database/sql"
	"fmt"

	"github.com/beevik/etree"
)

// XML default table name and row name
var (
	TableName = "RowData"
	RowName   = "Row_"
)

// RowsToMap transfer sql res to map
// dataMap[rowCnt]map[colName]value
func RowsToMap(rows *sql.Rows) (dataRes map[int]map[string]string, err error) {
	dataMap := make(map[int]map[string]string)
	cols, err := rows.Columns()
	if err != nil {
		return
	}

	rawResult := make([][]byte, len(cols))

	dest := make([]interface{}, len(cols)) // A temporary interface{} slice
	for i := range rawResult {
		dest[i] = &rawResult[i] // Put pointers to each string in the interface slice
	}

	rowCnt := 0
	for rows.Next() {
		rowMap := make(map[string]string)
		err = rows.Scan(dest...)
		if err != nil {
			return
		}

		for i, raw := range rawResult {
			rowMap[cols[i]] = string(raw)
		}
		dataMap[rowCnt] = rowMap

		rowCnt = rowCnt + 1
	}

	if dataMap != nil {
		dataRes = dataMap
	}

	return
}

// RowsToXML transfer sql res to xml
// <?xml version="1.0" encoding="UTF-8"?>
// <[tableName]> (default "RowDAta")
//     <[rowName]0> (default "Row_0")
//         <[KEY1]>[VALUE1]</[KEY1]>
//         <[KEY2]>[VALUE2]</[KEY2]>
//     </[rowName]0>
// </[tableName]>
func RowsToXML(rows *sql.Rows) (xmlString string, err error) {
	cols, err := rows.Columns()
	if err != nil {
		return
	}

	xmlString = cols[0]
	rawResult := make([][]byte, len(cols))
	dest := make([]interface{}, len(cols))
	for i := range rawResult {
		dest[i] = &rawResult[i]
	}

	xml := etree.NewDocument()
	xml.CreateProcInst("xml", `version="1.0" encoding="UTF-8"`)
	rowData := xml.CreateElement(TableName)
	rowCnt := 0
	for rows.Next() {
		err = rows.Scan(dest...)
		if err != nil {
			return
		}

		rowElement := rowData.CreateElement(fmt.Sprintf("%s%d", RowName, rowCnt))
		for i, raw := range rawResult {
			colName := rowElement.CreateElement(cols[i])
			colName.CreateText(string(raw))
		}

		rowCnt = rowCnt + 1
	}

	xml.Indent(2)
	xmlString, err = xml.WriteToString()

	return
}
