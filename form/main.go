package main

import (
	"bytes"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"text/template"

	"github.com/brianvoe/gofakeit/v6"
	"github.com/go-chi/chi/v5"
	datastar "github.com/starfederation/datastar/sdk/go"
)

type Field struct {
	Id string
	Title string
	Description string
	Widget string
	Type string // "string", "number", "boolean", "array"
}

type Form []Field

var schema = Form{
	{
		Id:     "name",
		Title:  "Имя",
		Widget: "input_string",
		Type: "string",
	},	
	{
		Id:     "age",
		Title:  "Возраст",
		Widget: "input_number",
		Type: "number",
	},
	{
		Id:     "hobby",
		Title:  "Хобби",
		Widget: "lookup",
		Type : "string",
	},
	// {
	// 	Id:     "city",
	// 	Title:  "Город",
	// 	Widget: "lookup",
	// },
}

type LookupTmplData struct {
    List []LookupListItem
	Id string 
	HasMore bool
}

var templates *template.Template
var lookupAllItems []string

type LookupState struct {
	Offset int `json:"offset,omitempty"` 
	Limit int `json:"limit,omitempty"` 
	Search string `json:"search,omitempty"`
	Open bool `json:"open,omitempty"`
	Value any `json:"value,omitempty"`
}

type LookupListItem struct {
	Value  string
	IsLast bool
	Index int
}
func buildList(items []string) []LookupListItem {
	result := make([]LookupListItem, len(items))

	for i, v := range items {
		result[i] = LookupListItem{
			Value:  v,
			IsLast: i == len(items)-1,
			Index: i,
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
	r.Get("/fields/{field}/widgets/lookup/list", handleLookupList)
	r.Post("/fields/{field}/widgets/lookup/change", handleLookupChange)
	r.Post("/fields/{field}/reset", handleReset)
	r.Put("/submit", handleSubmit)
	r.Post("/reset", handleReset)

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

func fieldsTempls(f Form) string {
	var buf string

	for _, field := range f {

		buf += templ(field.Widget, field)
	}

	return buf
}

func handleForm(w http.ResponseWriter, r *http.Request) {
    err := templates.ExecuteTemplate(w, "layout", struct {
		Content string 
	}{
		Content: fieldsTempls(schema),
	})

    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func handleLookupList(w http.ResponseWriter, r *http.Request) {
	fieldId := chi.URLParam(r, "field")
	listId := fmt.Sprintf("%s-lookup-list", fieldId) 


	appSignals := struct {
		Fields map[string]LookupState `json:"fields,omitempty"`
	}{
		Fields: make(map[string]LookupState),
	}

	if err := datastar.ReadSignals(r, &appSignals); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	log.Println("Received app signals:", appSignals)

	lookupSignals := appSignals.Fields[fieldId]
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

    pageList := buildList(filteredItems[start:end])

	itemsData := LookupTmplData{
		List: pageList,
		Id: fieldId,
		HasMore: len(filteredItems) > end,
	}

	itemsRender := templ("lookup_items", itemsData)

	fmm := datastar.FragmentMergeModeAppend 

	if lookupSignals.Offset == 0 {
		// FIX  FragmentMergeModeInner работает некорректно с массивом 
		// поэтому очищаем список с помощью передачи пустого элемента
		// fmm = datastar.FragmentMergeModeInner
		sse.MergeFragments(templ("lookup_list", struct { Id string; SkipGetList bool }{ Id: fieldId, SkipGetList: true }),)
	}

	sse.MergeSignals([]byte(`{ fields: {` + fieldId + `: {"offset":` + strconv.Itoa(lookupSignals.Offset + lookupSignals.Limit) + `}}}`))

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

func handleReset(w http.ResponseWriter, r *http.Request) {
    fieldId := chi.URLParam(r, "field")
	sse := datastar.NewSSE(w, r)

    if fieldId == "" {
        for _, field := range schema {
            sse.MergeSignals([]byte(`{ fields: {"` + field.Id + `": {value: ''}}}`))
        }
    } else {
        sse.MergeSignals([]byte(`{ fields: {"` + fieldId + `": {value: ''}}}`))
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

func handleLookupChange(w http.ResponseWriter, r *http.Request) {
	fieldId := chi.URLParam(r, "field")

	appSignals := struct {
		Fields map[string]LookupState `json:"fields,omitempty"`
	}{
		Fields: make(map[string]LookupState),
	}

	if err := datastar.ReadSignals(r, &appSignals); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	
	log.Println("fieldId:", fieldId)
	log.Println("Received app signals:", appSignals)

	// lookupSignals := appSignals.Fields[fieldId]

	// sse := datastar.NewSSE(w, r)
}