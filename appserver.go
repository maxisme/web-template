package appserver

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/coreos/go-systemd/activation"
	"github.com/gorilla/mux"
	"github.com/tylerb/graceful"
	"gopkg.in/validator.v2"
	"html/template"
	"io/ioutil"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const rfc2822 = "Mon, 28 Jan 2013 14:30:00 +0500"

var requiredPaths = [4]string{
	"images/og_logo.png",
	"images/icon.ico",
	"images/logo.png",
	"templates/",
}

var (
	_, b, _, _ = runtime.Caller(0)
	basepath   = filepath.Dir(b)
)

type page struct {
	Name    string
	Content template.HTML
}

type Sparkle struct {
	Description string `validate:"nonzero"`
	Version     string `validate:"nonzero"`
}

type Recaptcha struct {
	Pub  string `validate:"nonzero"`
	Priv string `validate:"nonzero"`
}

type ProjectConfig struct {
	Name        string    `validate:"nonzero"`
	Host        string    `validate:"nonzero"`
	DmgPath     string    `validate:"nonzero"`
	KeyWords    string    `validate:"nonzero"`
	Description string    `validate:"nonzero"`
	Recaptcha   Recaptcha `validate:"nonzero"`
	Sparkle     Sparkle   `validate:"nonzero"`
}

type IndexData struct {
	Project *ProjectConfig
	Pages   []page
	Year    int
}

type TemplateData struct {
	Data interface{}
}

func filenameWithoutExtension(fn string) string {
	return strings.ToLower(strings.TrimSuffix(path.Base(fn), path.Ext(fn)))
}

func replace(input, from, to string) string {
	return strings.Replace(input, from, to, -1)
}

func renderDirectory(pattern string, data interface{}) []page {
	var pages []page

	files, err := filepath.Glob(pattern)
	if err != nil {
		panic(err.Error())
	}

	for _, file := range files {
		ts, err := template.ParseFiles(file)
		if err != nil {
			panic(err.Error())
		}
		var tpl bytes.Buffer
		if err := ts.Execute(&tpl, TemplateData{data}); err != nil {
			panic(err.Error())
		}
		pages = append(pages, page{
			Name:    filenameWithoutExtension(file),
			Content: template.HTML(tpl.String()),
		})
	}
	return pages
}

func (p *ProjectConfig) webHandler(w http.ResponseWriter, r *http.Request) {
	tmplPath := basepath + "/index.html"
	tmpl := template.Must(template.New("index.html").Funcs(template.FuncMap{
		"replace": replace,
	}).ParseFiles(tmplPath))

	var data interface{}

	switch r.Method {
	case "GET":
		data = r.URL.Query()
	case "POST":
		data, _ = ioutil.ReadAll(r.Body)
	default:
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(http.StatusText(http.StatusNotImplemented)))
		return
	}

	if err := tmpl.Execute(w, IndexData{
		Project: p,
		Pages:   renderDirectory("templates/*.html", data),
		Year:    time.Now().Year(),
	}); err != nil {
		panic(err.Error())
	}
}

func (p *ProjectConfig) siteMapHandler(w http.ResponseWriter, r *http.Request) {
	xml := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
	<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">
	<url>
	  <loc>https://%s/</loc>
	  <lastmod>2020-01-01T00:00:00+00:00</lastmod>
	  <changefreq>monthly</changefreq>
	</url>
	</urlset>
	`, p.Host)

	if _, err := w.Write([]byte(xml)); err != nil {
		panic(err.Error())
	}
}

func (p *ProjectConfig) versionHandler(w http.ResponseWriter, r *http.Request) {
	// get age of dmg
	info, err := os.Stat(p.DmgPath)
	if err != nil {
		panic(err.Error())
		return
	}
	dmgTime := info.ModTime().Format(rfc2822)

	xml := fmt.Sprintf(`<?xml version="1.1" encoding="utf-8"?>
	<rss version="1.1" xmlns:Sparkle="https://%[1]s/xml-namespaces/Sparkle" xmlns:dc="https://%[1]s/dc/elements/1.1/">
	  <channel>
		<item>
			<title>Version %[2]s</title>
			<description><![CDATA[
				%[3]s
			]]>
			</description>
			<Sparkle:version>%[2]s</Sparkle:version>
			<pubDate>%[4]s</pubDate>
			<enclosure url="https://%[1]s/download" Sparkle:version="%[2]s"/>
		</item>
	  </channel>
	</rss>
	`, p.Host, p.Sparkle.Version, p.Sparkle.Description, dmgTime)

	if _, err := w.Write([]byte(xml)); err != nil {
		panic(err.Error())
	}
}

func (p *ProjectConfig) downloadHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Disposition", "attachment; filename=\""+p.DmgPath+"\"")
	http.ServeFile(w, r, p.DmgPath)
}

// Serve runs the web server based on the config in ProjectConfig
func Serve(p ProjectConfig) error {
	// validate requirements to start server
	if err := validator.Validate(p); err != nil { // validate config
		return err
	}
	for _, requiredPath := range requiredPaths { // insure paths exist
		if _, err := os.Stat(requiredPath); os.IsNotExist(err) {
			return errors.New(requiredPath + " required")
		}
	}
	if _, err := os.Stat(p.DmgPath); os.IsNotExist(err) { // insure valid dmg
		return errors.New(p.DmgPath + "doesn't exist")
	}

	// start server
	m := mux.NewRouter()
	m.HandleFunc("/", p.webHandler)
	m.HandleFunc("/sitemap", p.siteMapHandler)
	m.HandleFunc("/version", p.versionHandler)
	m.HandleFunc("/download", p.downloadHandler)
	m.PathPrefix("/images/").Handler(http.FileServer(http.Dir(".")))
	m.PathPrefix("/").Handler(http.FileServer(http.Dir(basepath + "/static/")))

	listeners, err := activation.Listeners()
	if err == nil && len(listeners) == 1 {
		return graceful.Serve(&http.Server{Handler: m}, listeners[0], 5*time.Second)
	}

	return graceful.ListenAndServe(&http.Server{Addr: ":9000", Handler: m}, 5*time.Second)
}
