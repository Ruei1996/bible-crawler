// Package biblecom — books.go
//
// books.go contains the canonical 66-book Protestant Bible catalogue used by
// the crawler to map book indices to USFM codes and localised book names.
// USFM codes are embedded directly in bible.com page URLs (e.g.
// /bible/111/GEN.1.NIV), so any change here would silently alter every URL
// the scraper generates — edit with care and re-run the full crawl afterwards.
package biblecom

// BookEntry maps a canonical book sort order to its USFM code and localised names.
// The USFM (Unified Standard Format Markers) code is the three-letter identifier
// used in bible.com URLs, e.g. /bible/111/GEN.1.NIV.
type BookEntry struct {
	Sort   int    // canonical 1–66 (matches bibles.bible_books.sort in DB)
	USFM   string // USFM abbreviation used in bible.com page URLs
	NameZH string // CUNP-上帝 Chinese book title
	NameEN string // NIV English book title
}

// Books is the complete, canonical list of 66 Bible books ordered by their
// standard Protestant canon sequence (OT books 1-39, NT books 40-66).
// These match the sort column in the bibles.bible_books PostgreSQL table.
var Books = []BookEntry{
	// ── Old Testament (39 books) ─────────────────────────────────────────────
	{1, "GEN", "創世記", "Genesis"},
	{2, "EXO", "出埃及記", "Exodus"},
	{3, "LEV", "利未記", "Leviticus"},
	{4, "NUM", "民數記", "Numbers"},
	{5, "DEU", "申命記", "Deuteronomy"},
	{6, "JOS", "約書亞記", "Joshua"},
	{7, "JDG", "士師記", "Judges"},
	{8, "RUT", "路得記", "Ruth"},
	{9, "1SA", "撒母耳記上", "1 Samuel"},
	{10, "2SA", "撒母耳記下", "2 Samuel"},
	{11, "1KI", "列王紀上", "1 Kings"},
	{12, "2KI", "列王紀下", "2 Kings"},
	{13, "1CH", "歷代志上", "1 Chronicles"},
	{14, "2CH", "歷代志下", "2 Chronicles"},
	{15, "EZR", "以斯拉記", "Ezra"},
	{16, "NEH", "尼希米記", "Nehemiah"},
	{17, "EST", "以斯帖記", "Esther"},
	{18, "JOB", "約伯記", "Job"},
	{19, "PSA", "詩篇", "Psalms"},
	{20, "PRO", "箴言", "Proverbs"},
	{21, "ECC", "傳道書", "Ecclesiastes"},
	{22, "SNG", "雅歌", "Song of Songs"},
	{23, "ISA", "以賽亞書", "Isaiah"},
	{24, "JER", "耶利米書", "Jeremiah"},
	{25, "LAM", "耶利米哀歌", "Lamentations"},
	{26, "EZK", "以西結書", "Ezekiel"},
	{27, "DAN", "但以理書", "Daniel"},
	{28, "HOS", "何西阿書", "Hosea"},
	{29, "JOL", "約珥書", "Joel"},
	{30, "AMO", "阿摩司書", "Amos"},
	{31, "OBA", "俄巴底亞書", "Obadiah"},
	{32, "JON", "約拿書", "Jonah"},
	{33, "MIC", "彌迦書", "Micah"},
	{34, "NAM", "那鴻書", "Nahum"},
	{35, "HAB", "哈巴谷書", "Habakkuk"},
	{36, "ZEP", "西番雅書", "Zephaniah"},
	{37, "HAG", "哈該書", "Haggai"},
	{38, "ZEC", "撒迦利亞書", "Zechariah"},
	{39, "MAL", "瑪拉基書", "Malachi"},

	// ── New Testament (27 books) ─────────────────────────────────────────────
	{40, "MAT", "馬太福音", "Matthew"},
	{41, "MRK", "馬可福音", "Mark"},
	{42, "LUK", "路加福音", "Luke"},
	{43, "JHN", "約翰福音", "John"},
	{44, "ACT", "使徒行傳", "Acts"},
	{45, "ROM", "羅馬書", "Romans"},
	{46, "1CO", "哥林多前書", "1 Corinthians"},
	{47, "2CO", "哥林多後書", "2 Corinthians"},
	{48, "GAL", "加拉太書", "Galatians"},
	{49, "EPH", "以弗所書", "Ephesians"},
	{50, "PHP", "腓立比書", "Philippians"},
	{51, "COL", "歌羅西書", "Colossians"},
	{52, "1TH", "帖撒羅尼迦前書", "1 Thessalonians"},
	{53, "2TH", "帖撒羅尼迦後書", "2 Thessalonians"},
	{54, "1TI", "提摩太前書", "1 Timothy"},
	{55, "2TI", "提摩太後書", "2 Timothy"},
	{56, "TIT", "提多書", "Titus"},
	{57, "PHM", "腓利門書", "Philemon"},
	{58, "HEB", "希伯來書", "Hebrews"},
	{59, "JAS", "雅各書", "James"},
	{60, "1PE", "彼得前書", "1 Peter"},
	{61, "2PE", "彼得後書", "2 Peter"},
	{62, "1JN", "約翰一書", "1 John"},
	{63, "2JN", "約翰二書", "2 John"},
	{64, "3JN", "約翰三書", "3 John"},
	{65, "JUD", "猶大書", "Jude"},
	{66, "REV", "啟示錄", "Revelation"},
}
