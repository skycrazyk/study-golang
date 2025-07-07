package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/go-chi/chi/v5"
	datastar "github.com/starfederation/datastar/sdk/go"
)

type AppSignals = struct {
	Fields map[string]any `json:"fields,omitempty"`
}

type Field struct {
	Id string
	Title string
	Description string
	Widget string
	Type string // "string", "number", "boolean", "array"
}

type Form []Field

type Widget struct {
	Reset func(w http.ResponseWriter, r *http.Request, fieldId *string, sse *datastar.ServerSentEventGenerator, appSignals *AppSignals)
}

type Widgets map[string]Widget

var widgets = Widgets{
	"lookup": {
		Reset: lookupReset,
	},
	"input_string": {
		Reset: inputStringReset,
	},
}

var schema = Form{
	{
		Id:     "name",
		Title:  "Имя (string)",
		Widget: "input_string",
		Type: "string",
		Description: "Виджет input_string",
	},	
	{
		Id:     "age",
		Title:  "Возраст (number)",
		Widget: "input_number",
		Type: "number",
		Description: "Виджет input_number",
	},
	{
		Id:     "hobby",
		Title:  "Хобби (string)",
		Widget: "lookup",
		Type : "string",
		Description: "Виджет Lookup",
	},
	{
		Id:     "city",
		Title:  "Города ([ ]string)",
		Widget: "lookup",
		Type: "array",
		Description: "Виджет Lookup",
	},
}

type LookupTmplData struct {
    List []LookupListItem
	Id string 
	HasMore bool
	Type string
}

var templates *template.Template
var lookupAllItems []string

type LookupState struct {
	Offset int `json:"offset,omitempty"` 
	Limit int `json:"limit,omitempty"` 
	Search string `json:"search,omitempty"`
	Open bool `json:"open,omitempty"`
	Value any `json:"value,omitempty"`
	AddValue any `json:"addValue,omitempty"`
	RemoveByIndex int `json:"removeByIndex,omitempty"` // индекс удаляемого значения
	RemoveByValue any `json:"removeByValue,omitempty"` // индекс удаляемого значения
}

type LookupListItem struct {
	Value  string
	ValueId string
	IsLast bool
	Index int
	Selected bool 
	Id string
	Type string
	HasMore bool 
}

func buildLookupItemList(items []string, field *Field, lookupSignals *LookupState, HasMore bool) []LookupListItem {
	result := make([]LookupListItem, len(items))

	for i, v := range items {
		result[i] = LookupListItem{
			Value:  v,
			ValueId: strings.ToLower(strings.NewReplacer(" ", "", ".", "", "-", "").Replace(v)),
			IsLast: i == len(items)-1,
			Index: i,
			Selected: (field.Type == "string" && v == lookupSignals.Value) ||
				(field.Type == "array" && func() bool {
					if arr, ok := lookupSignals.Value.([]any); ok {
						for _, item := range arr {
							if s, ok := item.(string); ok && s == v {
								return true
							}
						}
					}
					return false
				}()),
			Id: field.Id,
			Type: field.Type,
			HasMore: HasMore,
		}
	}

	return result
}

// contains returns true if substr is found in s (case-insensitive)
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}

func dict(values ...interface{}) map[string]interface{} {
	m := make(map[string]interface{})

	for i := 0; i < len(values); i += 2 {
		key := values[i].(string)
		m[key] = values[i+1]
	}

	return m
}

var funcs = template.FuncMap{
    "dict": dict,
}

var tmpl = template.New("").Funcs(funcs)

func main() {
	gofakeit.Seed(111)

	for range 100 {
		item := gofakeit.Hobby()
		lookupAllItems = append(lookupAllItems, item)
		// log.Println(i, item)
	}

    // Загружаем все шаблоны
    templates = template.Must(tmpl.ParseGlob("templates/*.html"))

    r := chi.NewRouter()
    r.Get("/", handleForm)
	r.Put("/submit", handleSubmit)
	r.Post("/reset", handleReset)
	r.Post("/fields/{field}/reset", handleReset)

	r.Get("/fields/{field}/widgets/lookup/list", handleLookupList)
	r.Post("/fields/{field}/widgets/lookup/add", handleLookupAdd)
	r.Post("/fields/{field}/widgets/lookup/reset", handleLookupReset)

	port := os.Getenv("PORT")

	if port == "" {
		port = "8080" // Значение по умолчанию
	}

    log.Println("Сервер запущен на http://localhost:" + port)
    http.ListenAndServe(":" + port, r)
}

func templ (name string, data any) string {
	var buf bytes.Buffer

	if err := templates.ExecuteTemplate(&buf, name, data); err != nil {
		panic(err)
	}

	return buf.String()
}

func fieldsTempls(f Form) template.HTML {
	var buf string

	for _, field := range f {

		buf += templ(field.Widget, field)
	}

	return template.HTML(buf)
}

func handleForm(w http.ResponseWriter, r *http.Request) {
    err := templates.ExecuteTemplate(w, "layout", struct {
		Content template.HTML 
	}{
		Content: fieldsTempls(schema),
	})

    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func handleLookupList(w http.ResponseWriter, r *http.Request) {
	appSignals, err := readAppSignals(w, r)

	if err != nil {
		http.Error(w, "Error reading appSignals", http.StatusBadRequest)
		return
	}

	field, lookupSignals := getLookupField(w, r, nil, appSignals)
	listId := fmt.Sprintf("%s-lookup-list", field.Id) 

	sse := datastar.NewSSE(w, r)

	filteredItems := lookupAllItems 

	if lookupSignals.Search != "" {
		filteredItems = make([]string, 0)

		for _, item := range lookupAllItems {
			if contains(item, lookupSignals.Search) {
				filteredItems = append(filteredItems, item)
			}
		}
	}

	var start, end = startEnd(lookupSignals.Offset, lookupSignals.Limit, len(filteredItems))

    pageList := buildLookupItemList(filteredItems[start:end], field, lookupSignals, len(filteredItems) > end)

	itemsData := LookupTmplData{
		Id: field.Id,
		Type: field.Type,
		List: pageList,
		HasMore: len(filteredItems) > end,
	}

	itemsRender := templ("lookup_items", itemsData)

	fmm := datastar.FragmentMergeModeAppend 

	if lookupSignals.Offset == 0 {
		// FIX  FragmentMergeModeInner работает некорректно с массивом 
		// поэтому очищаем список с помощью передачи пустого элемента
		// fmm = datastar.FragmentMergeModeInner
		sse.MergeFragments(templ("lookup_list", struct { Id string; SkipGetList bool }{ Id: field.Id, SkipGetList: true }),)
	}

	sse.MergeSignals([]byte(`{ fields: {` + field.Id + `: {"offset":` + strconv.Itoa(lookupSignals.Offset + lookupSignals.Limit) + `}}}`))

	sse.MergeFragments(
		itemsRender, 
		datastar.WithSelector(fmt.Sprintf(`#%s`, listId)), 
		datastar.WithMergeMode(fmm),
	)

	// log.Println("start: ", start, ", ", "end: ", end, ", ", "offset: ", lookupSignals.Offset,", ", "mode: ", fmm)
	
	// for i := range len(itemsData.List) {
	// 	log.Println("Item", i, ":", itemsData.List[i].Value)
	// }
}

func handleSubmit(w http.ResponseWriter, r *http.Request) {
	var formData map[string]any

	if err := datastar.ReadSignals(r, &formData); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Printf("Received form data: %+v\n", formData)

	sse := datastar.NewSSE(w, r)

	sse.MergeFragments(
		"<div id=\"form-status\">Форма успешно отправлена!</div>",
		datastar.WithSelector("#form"),
		datastar.WithMergeMode(datastar.FragmentMergeModeAfter),
	)
}

func readAppSignals(w http.ResponseWriter, r *http.Request) (*AppSignals, error)  {
	appSignals := AppSignals{
		Fields: make(map[string]any),
	}

	if err := datastar.ReadSignals(r, &appSignals); err != nil {
		return nil, err
	}

	return &appSignals, nil
}

func handleReset(w http.ResponseWriter, r *http.Request) {
    fieldId := chi.URLParam(r, "field")
	appSignals, err := readAppSignals(w, r)

	if err != nil {
		http.Error(w, "Error reading appSignals", http.StatusBadRequest)
		return 
	}

	sse := datastar.NewSSE(w, r)

    if fieldId == "" {
		for _, field := range schema {
			widget, ok := widgets[field.Widget]

			if ok && widget.Reset != nil {
				widget.Reset(w, r, &field.Id, sse, appSignals)
			} else {
				sse.MergeSignals([]byte(`{ fields: {"` + field.Id + `": {value: null }}}`))
			}
		}
    } else {
        sse.MergeSignals([]byte(`{ fields: {"` + fieldId + `": {value: null }}}`))
    }

	sse.RemoveFragments("#form-status")
}

func startEnd(offset int, limit int, total int) (int, int) {
	start := offset
	end := offset + limit

	if start > total {
		start = total
	}

	if end > total {
		end = total
	}

	return start, end
}

func getLookupField(w http.ResponseWriter, r *http.Request, fieldId *string, appSignals *AppSignals) (*Field, *LookupState) {
	var resolvedFieldId string

	if fieldId != nil {
		resolvedFieldId = *fieldId
	} else {
		resolvedFieldId = chi.URLParam(r, "field")
	}

	lookupSignalsAny := appSignals.Fields[resolvedFieldId]

	var field *Field

	for i := range schema {
		if schema[i].Id == resolvedFieldId {
			field = &schema[i]
			break
		}
	}

	if field == nil {
		http.Error(w, "Unknown field", http.StatusBadRequest)
		return nil, nil
	}

	var lookupSignals *LookupState
	switch v := lookupSignalsAny.(type) {
	case *LookupState:
		lookupSignals = v
	case LookupState:
		lookupSignals = &v
	case map[string]interface{}:
		// Convert map to JSON then unmarshal into LookupState
		b, _ := json.Marshal(v)
		var ls LookupState
		json.Unmarshal(b, &ls)
		lookupSignals = &ls
	case nil:
		lookupSignals = &LookupState{
			RemoveByIndex: -1,
		}
	default:
		lookupSignals = &LookupState{
			RemoveByIndex: -1,
		}
	}

	return field, lookupSignals
}
	
func handleLookupAdd(w http.ResponseWriter, r *http.Request) {
	appSignals, err := readAppSignals(w, r)
	field, lookupSignals := getLookupField(w, r, nil, appSignals)

	if field == nil || lookupSignals == nil || err != nil {
		http.Error(w, "Invalid field or lookup signals", http.StatusBadRequest)
		return
	}

	var nextValue any

	if field.Type == "array" {
		if arr, ok := lookupSignals.Value.([]any); ok {
			nextValue = append(arr, lookupSignals.AddValue)
		} else if lookupSignals.Value == nil {
			nextValue = []any{lookupSignals.AddValue}
		}
	} else {
		nextValue = lookupSignals.AddValue	
	}

	jsonValue, err := json.Marshal(nextValue)

	sse := datastar.NewSSE(w, r)


	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	sse.MergeSignals([]byte(fmt.Sprintf(`{ fields: {"%s": {value: %s}}}`, field.Id, string(jsonValue))))

	var mfs string
	var mfm datastar.FragmentMergeMode 

	var nextLen int

	switch v := nextValue.(type) {
	case []any:
		nextLen = len(v)
	default:
		nextLen = 1
	}

	if nextLen >= 2 {
		mfs = fmt.Sprintf("#%s-lookup-anchor > .badge:last-of-type", field.Id)
		mfm = datastar.FragmentMergeModeAfter
	} else {
		mfs = fmt.Sprintf("#%s-lookup-anchor", field.Id)
		sse.RemoveFragments(fmt.Sprintf("#%s-lookup-anchor > .badge", field.Id))
		mfm = datastar.FragmentMergeModePrepend
	}

	valueRender := templ("lookup_value", struct {
		Id    string
		Value string
		Type  string
	}{
		Id:    field.Id,
		Value: fmt.Sprintf("%v", lookupSignals.AddValue),
		Type:  field.Type,
	})

	sse.MergeFragments(
		valueRender,
		datastar.WithSelector(mfs),
		datastar.WithMergeMode(mfm),
	)
}

func handleLookupReset(w http.ResponseWriter, r *http.Request) {
	appSignals, err := readAppSignals(w, r)
	sse := datastar.NewSSE(w, r)

	if err != nil {
		http.Error(w, "Error reading appSignals", http.StatusBadRequest)
		return
	}

	lookupReset(w, r, nil, sse, appSignals)
}

func lookupReset(w http.ResponseWriter, r *http.Request, fieldId *string, sse *datastar.ServerSentEventGenerator, appSignals *AppSignals) {
	var field *Field
	var lookupSignals *LookupState

	if fieldId != nil {
		field, lookupSignals = getLookupField(w, r, fieldId, appSignals)
	} else {
		field, lookupSignals = getLookupField(w, r, nil, appSignals)
	}

	if field == nil || lookupSignals == nil {
		http.Error(w, "Invalid field or lookup signals", http.StatusBadRequest)
		return
	}
	
	if lookupSignals.RemoveByIndex != -1 {
		// Удаляем элемент по индексу
		if arr, ok := lookupSignals.Value.([]any); ok && lookupSignals.RemoveByIndex < len(arr) {
			newArr := append(arr[:lookupSignals.RemoveByIndex], arr[lookupSignals.RemoveByIndex+1:]...)

			if len(newArr) == 0 {
				lookupSignals.Value = nil
			} else {
				lookupSignals.Value = newArr
			}
		}

		sse.MarshalAndMergeSignals(map[string]map[string]any{
			"fields": {
				field.Id: map[string]any{
					"value": lookupSignals.Value,
					"removeByIndex": -1,
				},
			},
		})

		sse.RemoveFragments(fmt.Sprintf("#%s-lookup-anchor > .badge:nth-child(%d)", field.Id, lookupSignals.RemoveByIndex+1))

		return
	} else if lookupSignals.RemoveByValue != nil {
		// Удаляем соответствующий элемент из DOM
		removeIndex := -1
		
		log.Println("RemoveByValue: ",lookupSignals.RemoveByValue)

		if arr, ok := lookupSignals.Value.([]any); ok {
			for i, v := range arr {
				if v == lookupSignals.RemoveByValue {
					removeIndex = i
					break
				}
			}
		}
		
		// Удаляем элемент по значению
		if arr, ok := lookupSignals.Value.([]any); ok {
			newArr := make([]any, 0)
			for _, v := range arr {
				if v != lookupSignals.RemoveByValue {
					newArr = append(newArr, v)
				}
			}

			if len(newArr) == 0 {
				lookupSignals.Value = nil
			} else {
				lookupSignals.Value = newArr
			}
		}

		sse.MarshalAndMergeSignals(map[string]map[string]any{
			"fields": {
				field.Id: map[string]any{
					"value": lookupSignals.Value,
					"removeByIndex": -1,
					"removeByValue": nil,
				},
			},
		})

		sse.RemoveFragments(fmt.Sprintf("#%s-lookup-anchor > .badge:nth-child(%d)", field.Id, removeIndex+1))

		return
	}

	sse.MergeSignals([]byte(`{ fields: {"` + field.Id + `": {value: null, removeByIndex: -1, removeByValue: null}}}`))
	sse.RemoveFragments(fmt.Sprintf("#%s-lookup-anchor > .badge", field.Id))
}

func inputStringReset(w http.ResponseWriter, r *http.Request, fieldId *string, sse *datastar.ServerSentEventGenerator, appSignals *AppSignals) {
	sse.MergeSignals([]byte(`{ fields: {"` + *fieldId + `": {value: "" }}}`))
}
