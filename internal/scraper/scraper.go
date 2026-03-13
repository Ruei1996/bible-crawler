package scraper

import (
	"fmt"
	"log"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"bible-crawler/internal/repository"
	"bible-crawler/internal/utils"

	"github.com/PuerkitoBio/goquery"
	"github.com/gocolly/colly/v2"
	"github.com/google/uuid"
)

const (
	// LangChinese is the language key used in DB rows and crawler context.
	LangChinese = "chinese"
	// LangEnglish is the language key used in DB rows and crawler context.
	LangEnglish = "english"
)

// bibleChapterCounts is the definitive chapter count per book (index 0=Genesis … 65=Revelation).
// Sourced from the site's read100.html JavaScript: var cnum = new Array(...)
var bibleChapterCounts = [66]int{
	50, 40, 27, 36, 34, 24, 21, 4, 31, 24, // 0–9:  Gen–2Sam
	22, 25, 29, 36, 10, 13, 10, 42, 150, 31, // 10–19: 1Kgs–Prov
	12, 8, 66, 52, 5, 48, 12, 14, 3, 9, // 20–29: Eccl–Amos
	1, 4, 7, 3, 3, 3, 2, 14, 4, // 30–38: Oba–Mal
	28, 16, 24, 21, 28, 16, 16, 13, 6, 6, // 39–48: Matt–Eph
	4, 4, 5, 3, 6, 4, 3, 1, 13, 5, // 49–58: Phil–Jas
	5, 3, 5, 1, 1, 1, 22, // 59–65: 1Pet–Rev
}

// globalChapStarts[i] is the 1-based global chapter index for book i's first chapter.
// Genesis (0)=1, Exodus (1)=51, …, Revelation (65)=1168.
var globalChapStarts [66]int

func init() {
	offset := 1
	for i := 0; i < 66; i++ {
		globalChapStarts[i] = offset
		offset += bibleChapterCounts[i]
	}
}

type BibleScraper struct {
	Repo *repository.BibleRepository
	C    *colly.Collector
}

// BookMeta keeps minimal per-book data needed for later chapter crawling.
type BookMeta struct {
	ID    uuid.UUID
	Index int
	Name  string
}

// NewBibleScraper initializes a Colly collector with domain restriction and
// polite rate limiting so repeated runs remain stable against the source site.
func NewBibleScraper(repo *repository.BibleRepository) *BibleScraper {
	c := colly.NewCollector(
		colly.AllowedDomains("springbible.fhl.net"),
		colly.UserAgent("Mozilla/5.0 (compatible; BibleCrawler/1.0; +http://yourdomain.com)"),
		colly.Async(true),
		// Allow revisiting URLs so chapter 1 (fetched in discoverBooks) is
		// also processed in crawlChapters.
		colly.AllowURLRevisit(),
	)

	// Moderate rate limiting: 5 parallel requests, 200ms fixed + 100ms random delay.
	c.Limit(&colly.LimitRule{
		DomainGlob:  "*springbible.fhl.net*",
		Parallelism: 5,
		Delay:       200 * time.Millisecond,
		RandomDelay: 100 * time.Millisecond,
	})

	return &BibleScraper{
		Repo: repo,
		C:    c,
	}
}

// Run executes the two crawler phases.
// It returns an error when phase 1 cannot discover the expected book count,
// because phase 2 would otherwise produce incomplete data silently.
func (s *BibleScraper) Run() error {
	log.Println("Starting Bible Scraper...")
	books := s.discoverBooks()
	if len(books) != len(bibleChapterCounts) {
		return fmt.Errorf(
			"phase 1 incomplete: discovered %d/%d books; aborting chapter crawl",
			len(books),
			len(bibleChapterCounts),
		)
	}
	s.crawlChapters(books)
	return nil
}

// discoverBooks crawls all 66 book titles first and ensures each book row exists.
// This phase is intentionally separated so phase 2 can focus on chapter/verse data.
func (s *BibleScraper) discoverBooks() []BookMeta {
	log.Println("Phase 1: Discovering Books...")
	var books []BookMeta
	var mu sync.Mutex
	var wg sync.WaitGroup

	for i := 0; i < 66; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			meta, err := s.fetchBookMeta(index)
			if err != nil {
				log.Printf("Failed to fetch metadata for book index %d: %v", index, err)
				return
			}
			mu.Lock()
			books = append(books, meta)
			mu.Unlock()
		}(i)
	}

	wg.Wait()
	sort.Slice(books, func(i, j int) bool {
		return books[i].Index < books[j].Index
	})

	if len(books) != len(bibleChapterCounts) {
		log.Printf("Warning: discovered %d books, expected %d", len(books), len(bibleChapterCounts))
	}
	log.Printf("Discovered %d books.", len(books))
	return books
}

// fetchBookMeta resolves one Chinese title page and persists both Chinese/English
// metadata for that book.
func (s *BibleScraper) fetchBookMeta(bookIndex int) (BookMeta, error) {
	c := s.C.Clone()
	c.Async = false

	url := fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0", globalChapStarts[bookIndex])

	var meta BookMeta
	var errFetch error

	c.OnResponse(func(r *colly.Response) {
		body, err := utils.Big5ToUTF8(r.Body)
		if err != nil {
			errFetch = err
			return
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			errFetch = err
			return
		}

		bookTitle := fmt.Sprintf("Book_%d", bookIndex+1)
		doc.Find("font").Each(func(i int, sel *goquery.Selection) {
			text := strings.TrimSpace(sel.Text())
			if idx := strings.Index(text, " 第"); idx > 0 {
				bookTitle = strings.TrimSpace(text[:idx])
			}
		})

		bookID, err := s.Repo.GetOrCreateBook(bookIndex + 1)
		if err != nil {
			errFetch = err
			return
		}

		if err = s.Repo.UpsertBookContent(bookID, LangChinese, bookTitle); err != nil {
			errFetch = err
			return
		}

		if err = s.fetchEnglishTitle(bookIndex, bookID); err != nil {
			errFetch = err
			return
		}

		meta = BookMeta{
			ID:    bookID,
			Index: bookIndex,
			Name:  bookTitle,
		}
	})

	if err := c.Visit(url); err != nil {
		return BookMeta{}, err
	}
	if errFetch != nil {
		return BookMeta{}, errFetch
	}
	return meta, nil
}

// fetchEnglishTitle resolves and persists the English book title.
func (s *BibleScraper) fetchEnglishTitle(bookIndex int, bookID uuid.UUID) error {
	c := s.C.Clone()
	c.Async = false
	url := fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe", globalChapStarts[bookIndex])
	var errFetch error

	c.OnResponse(func(r *colly.Response) {
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(r.Body)))
		if err != nil {
			errFetch = fmt.Errorf("failed to parse english title page for book index %d: %w", bookIndex, err)
			return
		}

		// The English page font tag contains "BookName chapter N" (lowercase "chapter").
		reTitle := regexp.MustCompile(`(?i)(.*?)\s+chapter\s+\d+`)
		doc.Find("font").Each(func(i int, sel *goquery.Selection) {
			text := strings.TrimSpace(sel.Text())
			match := reTitle.FindStringSubmatch(text)
			if len(match) > 1 {
				title := strings.TrimSpace(match[1])
				if title != "" {
					if err := s.Repo.UpsertBookContent(bookID, LangEnglish, title); err != nil {
						errFetch = fmt.Errorf("failed to save english title for book index %d: %w", bookIndex, err)
					}
				}
			}
		})
	})

	if err := c.Visit(url); err != nil {
		return fmt.Errorf("failed to visit english title page for book index %d: %w", bookIndex, err)
	}
	if errFetch != nil {
		return errFetch
	}
	return nil
}

type chapterContext struct {
	bookID    uuid.UUID
	bookIndex int
	chapSort  int
	lang      string
}

// parseChapterContext validates per-request context values before DB writes.
func parseChapterContext(ctx *colly.Context) (chapterContext, error) {
	rawBookID := strings.TrimSpace(ctx.Get("bookID"))
	if rawBookID == "" {
		return chapterContext{}, fmt.Errorf("missing bookID in request context")
	}
	bookID, err := uuid.Parse(rawBookID)
	if err != nil {
		return chapterContext{}, fmt.Errorf("invalid bookID %q: %w", rawBookID, err)
	}

	rawBookIndex := strings.TrimSpace(ctx.Get("bookIndex"))
	bookIndex, err := strconv.Atoi(rawBookIndex)
	if err != nil {
		return chapterContext{}, fmt.Errorf("invalid bookIndex %q: %w", rawBookIndex, err)
	}
	if bookIndex < 0 || bookIndex >= len(bibleChapterCounts) {
		return chapterContext{}, fmt.Errorf("bookIndex out of range: %d", bookIndex)
	}

	rawChapSort := strings.TrimSpace(ctx.Get("chapSort"))
	chapSort, err := strconv.Atoi(rawChapSort)
	if err != nil {
		return chapterContext{}, fmt.Errorf("invalid chapSort %q: %w", rawChapSort, err)
	}
	if chapSort <= 0 || chapSort > bibleChapterCounts[bookIndex] {
		return chapterContext{}, fmt.Errorf(
			"chapter sort out of range for bookIndex=%d: chap=%d max=%d",
			bookIndex,
			chapSort,
			bibleChapterCounts[bookIndex],
		)
	}

	lang := strings.TrimSpace(ctx.Get("lang"))
	if lang != LangChinese && lang != LangEnglish {
		return chapterContext{}, fmt.Errorf("unsupported language in context: %q", lang)
	}

	return chapterContext{
		bookID:    bookID,
		bookIndex: bookIndex,
		chapSort:  chapSort,
		lang:      lang,
	}, nil
}

// crawlChapters asynchronously crawls every chapter page in both languages,
// then persists chapter + verse rows using repository-level idempotent writes.
func (s *BibleScraper) crawlChapters(books []BookMeta) {
	log.Println("Phase 2: Crawling Chapters...")

	c := s.C.Clone()

	c.OnResponse(func(r *colly.Response) {
		ctx, err := parseChapterContext(r.Ctx)
		if err != nil {
			log.Printf("Skipping response with invalid crawl context (url=%s): %v", r.Request.URL.String(), err)
			return
		}

		var body string
		if ctx.lang == LangChinese {
			body, err = utils.Big5ToUTF8(r.Body)
		} else {
			body = string(r.Body)
		}
		if err != nil {
			log.Printf("Encoding error for lang=%s chapter=%d: %v", ctx.lang, ctx.chapSort, err)
			return
		}

		chapID, err := s.Repo.GetOrCreateChapter(ctx.bookID, ctx.chapSort)
		if err != nil {
			log.Printf("DB error creating chapter row (bookID=%s chap=%d): %v", ctx.bookID, ctx.chapSort, err)
			return
		}

		chapTitle := fmt.Sprintf("第 %d 章", ctx.chapSort)
		if ctx.lang == LangEnglish {
			chapTitle = fmt.Sprintf("Chapter %d", ctx.chapSort)
		}
		if err = s.Repo.UpsertChapterContent(chapID, ctx.lang, chapTitle); err != nil {
			log.Printf("DB error saving chapter content (chapID=%s lang=%s): %v", chapID, ctx.lang, err)
		}

		doc, err := goquery.NewDocumentFromReader(strings.NewReader(body))
		if err != nil {
			log.Printf("GoQuery parse error (bookID=%s chap=%d): %v", ctx.bookID, ctx.chapSort, err)
			return
		}

		doc.Find("script, style, a").Remove()

		foundVerses := false
		doc.Find("ol li").Each(func(i int, sel *goquery.Selection) {
			verseNum := i + 1
			if valStr, exists := sel.Attr("value"); exists {
				if parsedVerseNum, parseErr := strconv.Atoi(strings.TrimSpace(valStr)); parseErr == nil && parsedVerseNum > 0 {
					verseNum = parsedVerseNum
				} else {
					log.Printf("Invalid verse number value %q, using fallback index %d", valStr, verseNum)
				}
			}

			content := utils.CleanText(sel.Text())
			if content == "" {
				return
			}

			if saveErr := s.saveVerse(ctx.bookID, chapID, verseNum, ctx.lang, content); saveErr != nil {
				log.Printf("Failed to save verse (bookID=%s chap=%d verse=%d lang=%s): %v",
					ctx.bookID, ctx.chapSort, verseNum, ctx.lang, saveErr)
				return
			}
			foundVerses = true
		})

		if !foundVerses {
			log.Printf("Warning: no verses found for bookID=%s chapter=%d lang=%s", ctx.bookID, ctx.chapSort, ctx.lang)
		}
	})

	for _, book := range books {
		if book.ID == uuid.Nil {
			log.Printf("Skipping book with nil ID (index=%d name=%s)", book.Index, book.Name)
			continue
		}
		if book.Index < 0 || book.Index >= len(bibleChapterCounts) {
			log.Printf("Skipping book with invalid index=%d name=%s", book.Index, book.Name)
			continue
		}

		maxChap := bibleChapterCounts[book.Index]
		for chap := 1; chap <= maxChap; chap++ {
			globalChap := globalChapStarts[book.Index] + chap - 1

			// Chinese (CUV – 和合本)
			ctxZH := colly.NewContext()
			ctxZH.Put("bookID", book.ID.String())
			ctxZH.Put("bookIndex", strconv.Itoa(book.Index))
			ctxZH.Put("chapSort", strconv.Itoa(chap))
			ctxZH.Put("lang", LangChinese)
			urlCUV := fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ft=0", globalChap)
			if err := c.Request("GET", urlCUV, nil, ctxZH, nil); err != nil {
				log.Printf("Failed to queue Chinese chapter request (book=%d chap=%d): %v", book.Index+1, chap, err)
			}

			// English (BBE – Basic English Bible)
			ctxEN := colly.NewContext()
			ctxEN.Put("bookID", book.ID.String())
			ctxEN.Put("bookIndex", strconv.Itoa(book.Index))
			ctxEN.Put("chapSort", strconv.Itoa(chap))
			ctxEN.Put("lang", LangEnglish)
			urlBBE := fmt.Sprintf("https://springbible.fhl.net/Bible2/cgic201/read201.cgi?na=0&chap=%d&ver=bbe", globalChap)
			if err := c.Request("GET", urlBBE, nil, ctxEN, nil); err != nil {
				log.Printf("Failed to queue English chapter request (book=%d chap=%d): %v", book.Index+1, chap, err)
			}
		}
	}

	c.Wait()
}

// saveVerse persists one verse row and its localized content.
func (s *BibleScraper) saveVerse(bookID, chapID uuid.UUID, verseNum int, lang, content string) error {
	if verseNum <= 0 {
		return fmt.Errorf("invalid verse number %d", verseNum)
	}
	normalizedContent := utils.CleanText(content)
	if normalizedContent == "" {
		return fmt.Errorf("empty verse content")
	}

	verseTitle := fmt.Sprintf("第%d節", verseNum)
	if lang == LangEnglish {
		verseTitle = fmt.Sprintf("verse %d", verseNum)
	}

	secID, err := s.Repo.GetOrCreateSection(bookID, chapID, verseNum)
	if err != nil {
		return fmt.Errorf("failed to get/create section %d: %w", verseNum, err)
	}

	if err = s.Repo.UpsertSectionContent(secID, lang, verseTitle, normalizedContent); err != nil {
		return fmt.Errorf("failed to upsert section content %d: %w", verseNum, err)
	}
	return nil
}
