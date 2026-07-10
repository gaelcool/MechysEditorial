package main

import (
	"context"
	"database/sql"
	"html/template"
	"log"
	"net/http"
	"strings"

	_ "modernc.org/sqlite"
)

const dbPath = "data/MC.db"

type App struct {
	db        *sql.DB
	templates *template.Template
}

type Author struct {
	ID         int64
	Name       string
	Bio        string
	APA        string
	ContactURL string
}

type Work struct {
	Num       int
	ID        int64
	Title     string
	PrintDate string
	Source    string
	FilePath  string
}

type Book struct {
	ID          int64
	Slug        string
	Title       string
	Description string
	PrintDate   string
	Cover       string
	MediaType   string
	FilePath    string
	Authors     []AuthorLink
	AuthorsText string
}

type HomePageData struct {
	Books []Book
}

type AuthorPageData struct {
	Author Author
	Works  []Work
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

func (a *App) queryHomePage(ctx context.Context) (*HomePageData, error) {
	rows, err := a.db.QueryContext(ctx, `SELECT
	b.id,
	b.slug,
	b.title,
	COALESCE(b.description, ''),
	b.print_date,
	COALESCE(b.cover, ''),
	COALESCE(b.media_type, ''),
	COALESCE(b.file_path, ''),
	COALESCE(author.id, 0),
	COALESCE(author.name, '')
FROM books b
LEFT JOIN book_authors ba ON ba.book_id = b.id
ORDER BY b.print_date`)
	// queries db into rows
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var books []Book
	bookIndex := map[int64]int{}

	for rows.Next() {
		var book Book
		var author AuthorLink
		if err := rows.Scan(
			&book.ID,
			&book.Slug,
			&book.Title,
			&book.Description,
			&book.PrintDate,
			&book.Cover,
			&book.MediaType,
			&book.FilePath,
			&author.ID,
			&author.Name,
		); err != nil {
			return nil, err
		}

		i, ok := bookIndex[book.ID]
		if !ok {
			bookIndex[book.ID] = len(books)
			books = append(books, book)
			i = len(books) - 1
		}
		if author.ID != 0 {
			books[i].Authors = append(books[i].Authors, author)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	for i := range books {
		names := make([]string, 0, len(books[i].Authors))
		for _, author := range books[i].Authors {
			names = append(names, author.Name)
		}
		books[i].AuthorsText = strings.Join(names, ", ")
	}

	return &HomePageData{Books: books}, nil
}

func (a *App) handleHome(w http.ResponseWriter, r *http.Request) {
	data, err := a.queryHomePage(r.Context())
	if err != nil {
		log.Printf("query home page: %v", err)
		http.Redirect(w, r, "/failed.html", http.StatusSeeOther)
		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, "index.html", data); err != nil {
		log.Printf("render home page: %v", err)
		http.Redirect(w, r, "/failed.html", http.StatusSeeOther)
		return
	}
}

func (a *App) queryAuthorPage(ctx context.Context, authorID string) (*AuthorPageData, error) {
	r, err := a.db.QueryContext(ctx, `SELECT
  id,
  name,
  COALESCE(bio, ''),
  COALESCE(apa, ''),
  COALESCE(contact_url, '')
  FROM authors
 WHERE id = ?`, authorID)
	if err != nil {
		return nil, err
	}
	defer r.Close()

	var author []Author
	for r.Next() {
		var a Author
		if err := r.Scan(&a.ID, &a.Name, &a.Bio, &a.APA, &a.ContactURL); err != nil {
			return nil, err
		}
		author = append(author, a)
	}
	if err := r.Err(); err != nil {
		return nil, err
	}
	if len(author) == 0 {
		return nil, sql.ErrNoRows
	}
	return &AuthorPageData{Author: author[0], Works: []Work{}}, nil
}

func (a *App) handleAuthor(w http.ResponseWriter, r *http.Request) {
	authorID := r.URL.Query().Get("id")
	if authorID == "" {
		http.Redirect(w, r, "/failed.html", http.StatusSeeOther)
		return
	}
	data, err := a.queryAuthorPage(r.Context(), authorID)
	if err != nil {
		http.Redirect(w, r, "/failed.html", http.StatusSeeOther)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := a.templates.ExecuteTemplate(w, "author_page.html", data); err != nil {
		http.Redirect(w, r, "/failed.html", http.StatusSeeOther)
		return
	}
}

func (a *App) handleBooks(w http.ResponseWriter, r *http.Request) {
	// Placeholder for handling books; implement as needed
	http.ServeFile(w, r, "books.html")
}

func main() {
	mux := http.NewServeMux()

	app, err := newApp(dbPath)
	if err != nil {
		log.Fatal(err)
	}
	defer app.db.Close()

	mux.HandleFunc("GET /{$}", app.handleHome)
	mux.HandleFunc("GET /authors", app.handleAuthor)
	mux.HandleFunc("GET /books", app.handleBooks)
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
