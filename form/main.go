package main

import (
	"bytes"
	html "html/template"
	"log"
	"net/http"
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
	Widget string
}

type Form []Field

var schema = Form{
	{
		Id:     "name",
		Title:  "Имя",
		Widget: "input_string",
	},	
	{
		Id:     "age",
		Title:  "Возраст",
		Widget: "input_number",
	},
	{
		Id:     "hobby",
		Title:  "Хобби",
		Widget: "lookup",
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
}
func buildList(items []string) []LookupListItem {
	result := make([]LookupListItem, len(items))

	for i, v := range items {
		result[i] = LookupListItem{
			Value:  v,
			IsLast: i == len(items)-1,
		}
	}

	return result
}

// contains returns true if substr is found in s (case-insensitive)
func contains(s, substr string) bool {
	return strings.Contains(strings.ToLower(s), strings.ToLower(substr))
}


func main() {
	gofakeit.Seed(111)

	for range 100 {
		lookupAllItems = append(lookupAllItems,  gofakeit.Hobby())
	}
	
 	// Загружаем все шаблоны
    templates = template.Must(template.ParseGlob("templates/*.html"))

    r := chi.NewRouter()
    r.Get("/", handleForm)
	r.Get("/fields/{field}/widgets/lookup/list", handleLookupList)
	r.Put("/submit", handleSubmit)
	r.Post("/reset", handleReset)

    log.Println("Сервер запущен на http://localhost:8080")
    http.ListenAndServe(":8080", r)
}

func fieldsTempls(f Form) html.HTML {
	var buf bytes.Buffer

	for _, field := range f {
		if err := templates.ExecuteTemplate(&buf, field.Widget, field); err != nil {
			panic(err)
		}
	}

	return html.HTML(buf.String())
}

func handleForm(w http.ResponseWriter, r *http.Request) {
    err := templates.ExecuteTemplate(w, "layout", struct {
		Content html.HTML
	}{
		Content: fieldsTempls(schema),
	})

    if err != nil {
        http.Error(w, err.Error(), http.StatusInternalServerError)
    }
}

func handleLookupList(w http.ResponseWriter, r *http.Request) {
	fieldId := chi.URLParam(r, "field")
	store := make(map[string]LookupState) 

	if err := datastar.ReadSignals(r, &store); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	signals := store[fieldId]
	sse := datastar.NewSSE(w, r)

	filteredItems := lookupAllItems 

	if signals.Value != "" {
		filteredItems = make([]string, 0)

		for _, item := range lookupAllItems {
			if contains(item, signals.Value.(string)) {
				filteredItems = append(filteredItems, item)
			}
		}
	}

    start := signals.Offset
    end := signals.Offset + signals.Limit

    if start > len(filteredItems) {
        start = len(filteredItems)
    }

    if end > len(filteredItems) {
        end = len(filteredItems)
    }

    pageList := buildList(filteredItems[start:end])

	itemsData := LookupTmplData{
		List: pageList,
		Id: fieldId,
	}

	var buf bytes.Buffer

	if err := templates.ExecuteTemplate(&buf, "lookup_items", itemsData); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	fmm := datastar.FragmentMergeModeAppend 

	if signals.Offset == 0 {
		fmm = datastar.FragmentMergeModeInner
	}

	sse.MergeSignals([]byte(`{` + fieldId + `: {"offset":` + strconv.Itoa(signals.Offset + signals.Limit) + `}}`))

	sse.MergeFragments(
		buf.String(), 
		datastar.WithSelector(`#` + fieldId + `-lookup-list`), 
		datastar.WithMergeMode(fmm),
	)
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
	sse := datastar.NewSSE(w, r)

	for _, field := range schema {
		sse.MergeSignals([]byte(`{"` + field.Id + `": {value: ''}}`))
	}

	sse.RemoveFragments("#form-status")
}