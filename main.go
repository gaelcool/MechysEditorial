package main

import (
	"context"
	"database/sql"
	"html/template"
	"log"
	"net/http"

	_ "modernc.org/sqlite"
)

const dbPath = "data/MC.db"

type App struct {
	db        *sql.DB
	templates *template.Template
}

type AuthorLink struct {
	ID   int64
	Name string
}

func openDB(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}

	if _, err := db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func newApp(path string) (*App, error) {
	db, err := openDB(path)
	if err != nil {
		return nil, err
	}

	tmpl, err := template.
		ParseFiles(
			"index.html",
			"author_page.html",
			//"author_links.html", wip
		)
	if err != nil {
		db.Close()
		return nil, err
	}

	return &App{
		db:        db,
		templates: tmpl,
	}, nil
}

type Book struct {
	ID          int64
	Title       string
	Description string
	PrintDate   string
	Cover       string
	Authors     []AuthorLink
	FilePath    string
}

type HomePage struct {
	Books []Book
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data, err := a.queryHomePage(r.Context())
	if err != nil {
		log.Printf("query books fragment: %v", err)
		http.Error(w, "No se pudo cargar el catalogo.", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render books fragment: %v", err)
	}
}

func (a *App) queryHomePage(ctx context.Context) (*HomePage, error) {
	rows, err := a.db.QueryContext(ctx,
		`SELECT b.id, b.title, b.description,
		b.print_date, COALESCE(b.cover, ''),
		COALESCE(group_concat(a.name, ',')
		AS authors,
		b.file_path
		FROM books AS b
		LEFT JOIN book_authors AS ba ON ba.book_id = b.id
		Left join AUTHORS as A on A.ID = BA.AUThor_id
		GROUP BY b.id
		`)
	if err != nil {
		defer rows.Close()
		return nil, err
	}
	var books []Book

	for rows.Next() {
		var book Book
		var author AuthorLink
		rows.Scan(
			&book.ID,
			&book.Title,
			&book.Description,
			&book.PrintDate,
			&book.Cover,
			&author.ID,
			&author.Name,
			&book.FilePath,
		)
		books = append(books, book)
	}
	// In theory, rows is an asociative array holding key value pairs for a books characteristics
	return &HomePage{Books: books}, nil
}

func main() {
	mux := http.NewServeMux()

	app, err := newApp(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()

	mux.HandleFunc("GET /{$}", app.handleHome)
	// mux.HandleFunc("GET /authors", app.handleAuthor)
	mux.Handle("GET /books/", http.StripPrefix("/", http.FileServer(http.Dir("."))))
	mux.HandleFunc("GET /favicon.png", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "favicon.png")
	})

	mux.HandleFunc("GET /failed.html", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFile(w, r, "failed.html")
	})

	mux.Handle("/", http.FileServer(http.Dir("public")))

	log.Println("Listening on :7200")
	log.Fatal(http.ListenAndServe(":7200", mux))
}
