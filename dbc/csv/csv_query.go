package csv

import (
	"encoding/csv"
	// "fmt"
	"github.com/eaciit/cast"
	"github.com/eaciit/crowd"
	"github.com/eaciit/dbox"
	"github.com/eaciit/errorlib"
	"github.com/eaciit/toolkit"
	"io"
	"os"
	"reflect"
	"regexp"
)

const (
	modQuery = "Query"
)

type Query struct {
	dbox.Query

	file     *os.File
	tempFile *os.File
	reader   *csv.Reader
	save     bool
}

type QueryCondition struct {
	Select toolkit.M
	Find   toolkit.M
	Sort   []string
	skip   int
	limit  int
}

func (w *QueryCondition) getCondition(dataCheck toolkit.M) bool {
	resBool := true

	if len(w.Find) > 0 {
		resBool = foundCondition(dataCheck, w.Find)
	}

	return resBool
}

func foundCondition(dataCheck toolkit.M, cond toolkit.M) bool {
	resBool := true

	for key, val := range cond {
		if key == "$and" || key == "$or" {
			for i, sVal := range val.([]interface{}) {
				rVal := sVal.(map[string]interface{})
				mVal := toolkit.M{}
				for rKey, mapVal := range rVal {
					mVal.Set(rKey, mapVal)
				}

				xResBool := foundCondition(dataCheck, mVal)
				if key == "$and" {
					resBool = resBool && xResBool
				} else {
					if i == 0 {
						resBool = xResBool
					} else {
						resBool = resBool || xResBool
					}
				}
			}
		} else {
			if reflect.ValueOf(val).Kind() == reflect.Map {
				mVal := val.(map[string]interface{})
				tomVal, _ := toolkit.ToM(mVal)
				switch {
				case tomVal.Has("$ne"):
					if tomVal["$ne"].(string) == dataCheck.Get(key, "").(string) {
						resBool = false
					}
				case tomVal.Has("$regex"):
					resBool, _ = regexp.MatchString(tomVal["$regex"].(string), dataCheck.Get(key, "").(string))
				case tomVal.Has("$gt"):
					if tomVal["$gt"].(string) >= dataCheck.Get(key, "").(string) {
						resBool = false
					}
				case tomVal.Has("$gte"):
					if tomVal["$gte"].(string) > dataCheck.Get(key, "").(string) {
						resBool = false
					}
				case tomVal.Has("$lt"):
					if tomVal["$lt"].(string) <= dataCheck.Get(key, "").(string) {
						resBool = false
					}
				case tomVal.Has("$lte"):
					if tomVal["$lte"].(string) < dataCheck.Get(key, "").(string) {
						resBool = false
					}
				}
			} else if reflect.ValueOf(val).Kind() == reflect.String && val != dataCheck.Get(key, "").(string) {
				resBool = false
			}
		}
	}

	return resBool
}

func (q *Query) File() *os.File {
	if q.file == nil {
		q.file = q.Connection().(*Connection).file
	}
	return q.file
}

func (q *Query) Reader() *csv.Reader {
	if q.reader == nil {
		q.reader = q.Connection().(*Connection).reader
	}
	return q.reader
}

func (q *Query) Close() {
	if q.save {
		_ = q.Connection().(*Connection).EndSessionWrite()
	}
}

func (q *Query) Prepare() error {
	return nil
}

func (q *Query) Cursor(in toolkit.M) (dbox.ICursor, error) {
	var e error

	aggregate := false

	if q.Connection().(*Connection).setNewHeader {
		q.Connection().(*Connection).Close()
		filename := q.Connection().(*Connection).Info().Host
		os.Remove(filename)
		return nil, errorlib.Error(packageName, "Cursor", modQuery, "Only Insert Query Permited")
	}

	parts := crowd.From(q.Parts()).Group(func(x interface{}) interface{} {
		qp := x.(*dbox.QueryPart)
		return qp.PartType
	}, nil).Data

	skip := 0
	if skipParts, hasSkip := parts[dbox.QueryPartSkip]; hasSkip {
		skip = skipParts.([]interface{})[0].(*dbox.QueryPart).
			Value.(int)
	}

	take := 0
	if takeParts, has := parts[dbox.QueryPartTake]; has {
		take = takeParts.([]interface{})[0].(*dbox.QueryPart).
			Value.(int)
	}

	var fields toolkit.M
	selectParts, hasSelect := parts[dbox.QueryPartSelect]
	if hasSelect {
		fields = toolkit.M{}
		for _, sl := range selectParts.([]interface{}) {
			qp := sl.(*dbox.QueryPart)
			for _, fid := range qp.Value.([]string) {
				fields.Set(fid, 1)
			}
		}
	} else {
		_, hasUpdate := parts[dbox.QueryPartUpdate]
		_, hasInsert := parts[dbox.QueryPartInsert]
		_, hasDelete := parts[dbox.QueryPartDelete]
		_, hasSave := parts[dbox.QueryPartSave]

		if hasUpdate || hasInsert || hasDelete || hasSave {
			return nil, errorlib.Error(packageName, modQuery, "Cursor",
				"Valid operation for a cursor is select only")
		}
	}

	var sort []string
	sortParts, hasSort := parts[dbox.QueryPartSelect]
	if hasSort {
		sort = []string{}
		for _, sl := range sortParts.([]interface{}) {
			qp := sl.(*dbox.QueryPart)
			for _, fid := range qp.Value.([]string) {
				sort = append(sort, fid)
			}
		}
	}

	var where interface{}
	whereParts, hasWhere := parts[dbox.QueryPartWhere]
	if hasWhere {
		fb := q.Connection().Fb()
		for _, p := range whereParts.([]interface{}) {
			fs := p.(*dbox.QueryPart).Value.([]*dbox.Filter)
			for _, f := range fs {
				fb.AddFilter(f)
			}
		}
		where, e = fb.Build()
		if e != nil {
			return nil, errorlib.Error(packageName, modQuery, "Cursor",
				e.Error())
		} else {
			//fmt.Printf("Where: %s", toolkit.JsonString(where))
		}
		//where = iwhere.(toolkit.M)
	}

	cursor := dbox.NewCursor(new(Cursor))
	cursor = cursor.SetConnection(q.Connection())

	cursor.(*Cursor).file = q.File()
	cursor.(*Cursor).reader = q.Reader()
	cursor.(*Cursor).headerColumn = q.Connection().(*Connection).headerColumn
	cursor.(*Cursor).count = 0
	// fmt.Println(cursor.(*Cursor).headerColumn)
	if e != nil {
		return nil, errorlib.Error(packageName, modQuery, "Cursor", e.Error())
	}

	if !aggregate {
		// fmt.Println("Query 173 : ", where)
		cursor.(*Cursor).ConditionVal.Find, _ = toolkit.ToM(where)

		if fields != nil {
			cursor.(*Cursor).ConditionVal.Select = fields
		}

		if hasSort {
			cursor.(*Cursor).ConditionVal.Sort = sort
		}
		cursor.(*Cursor).ConditionVal.skip = skip
		cursor.(*Cursor).ConditionVal.limit = take

	} else {
		/*		pipes := toolkit.M{}
				mgoPipe := session.DB(dbname).C(tablename).
					Pipe(pipes).AllowDiskUse()
				//iter := mgoPipe.Iter()

				cursor.(*Cursor).ResultType = QueryResultPipe
				cursor.(*Cursor).mgoPipe = mgoPipe
				//cursor.(*Cursor).mgoIter = iter
		*/
	}
	return cursor, nil
}

func (q *Query) Exec(parm toolkit.M) error {
	var e error
	q.save = false

	// useHeader := q.Connection().Info().Settings.Get("useheader", false).(bool)
	if parm == nil {
		parm = toolkit.M{}
	}

	data := parm.Get("data", nil)

	parts := crowd.From(q.Parts()).Group(func(x interface{}) interface{} {
		qp := x.(*dbox.QueryPart)
		return qp.PartType
	}, nil).Data

	// fromParts, hasFrom := parts[dbox.QueryPartFrom]
	// if !hasFrom {
	// 	return errorlib.Error(packageName, "Query", modQuery, "Invalid table name")
	// }
	// tablename = fromParts.([]interface{})[0].(*dbox.QueryPart).Value.(string)

	// var where interface{}
	commandType := ""
	//	multi := false

	_, hasDelete := parts[dbox.QueryPartDelete]
	_, hasInsert := parts[dbox.QueryPartInsert]
	_, hasUpdate := parts[dbox.QueryPartUpdate]
	_, hasSave := parts[dbox.QueryPartSave]

	if hasDelete {
		commandType = dbox.QueryPartDelete
	} else if hasInsert {
		commandType = dbox.QueryPartInsert
	} else if hasUpdate {
		commandType = dbox.QueryPartUpdate
	} else if hasSave {
		commandType = dbox.QueryPartSave
		q.save = true
	}

	var where interface{}
	whereParts, hasWhere := parts[dbox.QueryPartWhere]
	if hasWhere {
		fb := q.Connection().Fb()
		for _, p := range whereParts.([]interface{}) {
			fs := p.(*dbox.QueryPart).Value.([]*dbox.Filter)
			for _, f := range fs {
				fb.AddFilter(f)
			}
		}
		where, e = fb.Build()
		if e != nil {
			return errorlib.Error(packageName, modQuery, "Cursor", e.Error())
		}
	}

	//Check setNewHeader First
	if q.Connection().(*Connection).setNewHeader && commandType != dbox.QueryPartInsert {
		q.Connection().(*Connection).Close()
		filename := q.Connection().(*Connection).Info().Host
		os.Remove(filename)
		return errorlib.Error(packageName, "Query", modQuery, "Only Insert Permited")
	}

	q.Connection().(*Connection).TypeOpenFile = TypeOpenFile_Append
	if hasDelete || hasUpdate {
		q.Connection().(*Connection).TypeOpenFile = TypeOpenFile_Create
	}

	q.Connection().(*Connection).ExecOpr = false
	if !q.Connection().(*Connection).setNewHeader && (commandType != dbox.QueryPartSave || (commandType == dbox.QueryPartSave && q.Connection().(*Connection).writer == nil)) {
		e = q.Connection().(*Connection).StartSessionWrite()
	}

	if e != nil {
		return errorlib.Error(packageName, "Query", modQuery, e.Error())
	}

	writer := q.Connection().(*Connection).writer
	reader := q.Connection().(*Connection).reader

	var execCond QueryCondition
	execCond.Find, _ = toolkit.ToM(where)

	switch commandType {
	case dbox.QueryPartInsert, dbox.QueryPartSave:
		var dataTemp []string
		dataMformat, _ := toolkit.ToM(data)
		// fmt.Println("LINE338:", q.Connection().(*Connection).setNewHeader)
		if q.Connection().(*Connection).setNewHeader {
			q.Connection().(*Connection).SetHeaderToolkitM(dataMformat)
			q.Connection().(*Connection).setNewHeader = false

			for _, v := range q.Connection().(*Connection).headerColumn {
				dataTemp = append(dataTemp, v.name)
			}

			if len(dataTemp) > 0 {
				writer.Write(dataTemp)
				writer.Flush()
			}
			// fmt.Println("LINE342:", q.Connection().(*Connection).headerColumn)
			dataTemp = []string{}
		}

		for _, v := range q.Connection().(*Connection).headerColumn {
			if dataMformat.Has(v.name) {
				dataTemp = append(dataTemp, cast.ToString(dataMformat[v.name]))
			} else {
				dataTemp = append(dataTemp, "")
			}
		}

		if len(dataTemp) > 0 {
			writer.Write(dataTemp)
			writer.Flush()
		}
	case dbox.QueryPartDelete:
		var tempHeader []string

		for _, val := range q.Connection().(*Connection).headerColumn {
			tempHeader = append(tempHeader, val.name)
		}

		for {
			foundDelete := true
			recData := toolkit.M{}

			dataTemp, e := reader.Read()
			for i, val := range dataTemp {
				recData.Set(tempHeader[i], val)
			}

			foundDelete = execCond.getCondition(recData)

			if e == io.EOF {
				if !foundDelete && dataTemp != nil {
					writer.Write(dataTemp)
					writer.Flush()
				}
				break
			} else if e != nil {
				return errorlib.Error(packageName, modQuery, "Delete", e.Error())
			}

			if !foundDelete && dataTemp != nil {
				writer.Write(dataTemp)
				writer.Flush()
			}
		}
	case dbox.QueryPartUpdate:
		var tempHeader []string

		if data == nil {
			break
		}

		dataMformat, _ := toolkit.ToM(data)

		for _, val := range q.Connection().(*Connection).headerColumn {
			tempHeader = append(tempHeader, val.name)
		}

		for {
			foundChange := false

			recData := toolkit.M{}
			dataTemp, e := reader.Read()
			for i, val := range dataTemp {
				recData.Set(tempHeader[i], val)
			}

			foundChange = execCond.getCondition(recData)
			if foundChange && len(dataTemp) > 0 {
				for n, v := range tempHeader {
					if dataMformat.Has(v) {
						dataTemp[n] = cast.ToString(dataMformat[v])
					}
				}
			}

			if e == io.EOF {
				if dataTemp != nil {
					writer.Write(dataTemp)
					writer.Flush()
				}
				break
			} else if e != nil {
				return errorlib.Error(packageName, modQuery, "Update", e.Error())
			}
			if dataTemp != nil {
				writer.Write(dataTemp)
				writer.Flush()
			}
		}
	}

	q.Connection().(*Connection).ExecOpr = true
	if commandType != dbox.QueryPartSave {
		e = q.Connection().(*Connection).EndSessionWrite()
	}

	return nil
}
