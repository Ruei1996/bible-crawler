-- ============================================================
-- Bible Data Validation Script
-- ============================================================
-- Validates the bibles schema against the canonical 66-book spec.
--
-- Structure:
--   SECTION 0  — Global summary (counts & missing at every level)
--   SECTION 1  — Books  missing Chinese or English title
--   SECTION 2  — Chapters missing Chinese or English title
--   SECTION 3  — Sections (verses) missing Chinese or English content
--   SECTION 4  — Spec-driven structural check (expected vs actual chapters)
--   SECTION 5  — Versification-difference audit (ZH vs EN section counts)
--   SECTION 6  — Chapter content viewer: query by book name + chapter number
--   SECTION 7  — Missing verse finder: show only absent (verse, language) pairs
--               for a given book + chapter
--   SECTION 8  — Post-import verse count summary (quick totals per language)
--   SECTION 9  — Empty content guard (any row = import error; expect 0 rows)
--   SECTION 10 — Bracket-verse spot-check: the 16 NIV textually-disputed verses
--               that previously caused WARN/skipped; all must have non-empty
--               English content after the cross-reference resolution fix
--
-- A fully loaded database returns:
--   • 0 rows  in sections 1, 2, 3, 4, 5, 9, and all english_status = 'OK' in 10
--   • missing_chinese = 0 and missing_english = 0 in section 0
--   • chinese: 31,092 | english: 31,103 total_section_contents in section 8
-- ============================================================


-- ── SECTION 6 — Chapter content viewer (bilingual)
--
-- HOW TO USE:
--   1. Set @book_name  → Chinese (e.g. '創世記') OR English (e.g. 'Genesis')
--   2. Set @chapter_num → chapter number (e.g. 32)
--   3. Run the query — all verses in both languages appear, sorted correctly.
--
-- Sorting strategy:
--   • language: 'chinese' always comes before 'english'
--   • verse order: sorted by bible_sections.sort (integer), not by the title
--     string, because string-sorted "第10節" < "第2節" (ASCII order is wrong).
--     Integer sort gives the natural 1 → 2 → … → 60 order the user expects.
-- ============================================================


-- ── SECTION 0: Global summary ────────────────────────────────
-- Quick overview of completeness at every level.
-- Both language columns should equal the "total" column.

SELECT
    'Level 1: Books'                                                AS level,
    COUNT(DISTINCT bb.id)                                           AS total,
    COUNT(DISTINCT bbc_zh.id)                                       AS has_chinese,
    COUNT(DISTINCT bbc_en.id)                                       AS has_english,
    COUNT(DISTINCT bb.id) - COUNT(DISTINCT bbc_zh.id)               AS missing_chinese,
    COUNT(DISTINCT bb.id) - COUNT(DISTINCT bbc_en.id)               AS missing_english
FROM bibles.bible_books bb
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'

UNION ALL

SELECT
    'Level 2: Chapters',
    COUNT(DISTINCT bc.id),
    COUNT(DISTINCT bcc_zh.id),
    COUNT(DISTINCT bcc_en.id),
    COUNT(DISTINCT bc.id) - COUNT(DISTINCT bcc_zh.id),
    COUNT(DISTINCT bc.id) - COUNT(DISTINCT bcc_en.id)
FROM bibles.bible_chapters bc
LEFT JOIN bibles.bible_chapter_contents bcc_zh
       ON bcc_zh.bible_chapter_id = bc.id AND bcc_zh.language = 'chinese'
LEFT JOIN bibles.bible_chapter_contents bcc_en
       ON bcc_en.bible_chapter_id = bc.id AND bcc_en.language = 'english'

UNION ALL

SELECT
    'Level 3: Sections (Verses)',
    COUNT(DISTINCT bs.id),
    COUNT(DISTINCT bsc_zh.id),
    COUNT(DISTINCT bsc_en.id),
    COUNT(DISTINCT bs.id) - COUNT(DISTINCT bsc_zh.id),
    COUNT(DISTINCT bs.id) - COUNT(DISTINCT bsc_en.id)
FROM bibles.bible_sections bs
LEFT JOIN bibles.bible_section_contents bsc_zh
       ON bsc_zh.bible_section_id = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN bibles.bible_section_contents bsc_en
       ON bsc_en.bible_section_id = bs.id AND bsc_en.language = 'english';


-- ── SECTION 1: Books missing content ─────────────────────────
-- Expect 0 rows.
-- Lists every book that is missing a Chinese title, English title, or both.

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc_zh.title, '⚠ MISSING')                            AS title_zh,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS title_en,
    CASE WHEN bbc_zh.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS chinese_status,
    CASE WHEN bbc_en.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS english_status
FROM bibles.bible_books bb
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
WHERE bbc_zh.id IS NULL
   OR bbc_en.id IS NULL
ORDER BY bb.sort;


-- ── SECTION 2: Chapters missing content ──────────────────────
-- Expect 0 rows.
-- Lists every chapter that is missing a Chinese title, English title, or both.

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc_zh.title, '⚠ 缺書名')                             AS book_name_zh,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS book_name_en,
    bc.sort                                                         AS chapter_sort,
    COALESCE(bcc_zh.title, '⚠ MISSING')                            AS chapter_title_zh,
    COALESCE(bcc_en.title, '⚠ MISSING')                            AS chapter_title_en,
    CASE WHEN bcc_zh.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS chinese_status,
    CASE WHEN bcc_en.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS english_status
FROM bibles.bible_chapters bc
JOIN  bibles.bible_books bb
       ON bb.id = bc.bible_book_id
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
LEFT JOIN bibles.bible_chapter_contents bcc_zh
       ON bcc_zh.bible_chapter_id = bc.id AND bcc_zh.language = 'chinese'
LEFT JOIN bibles.bible_chapter_contents bcc_en
       ON bcc_en.bible_chapter_id = bc.id AND bcc_en.language = 'english'
WHERE bcc_zh.id IS NULL
   OR bcc_en.id IS NULL
ORDER BY bb.sort, bc.sort;


-- ── SECTION 3: Sections (verses) missing content ─────────────
-- Expect 0 rows.
-- Lists every verse-row that is missing Chinese content, English content, or both.
-- After running cmd/repair, versification-difference positions are covered by
-- placeholder rows, so this query should also return 0 rows.

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc_zh.title, '⚠ 缺書名')                             AS book_name_zh,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS book_name_en,
    bc.sort                                                         AS chapter_sort,
    bs.sort                                                         AS section_sort,
    COALESCE(bsc_zh.title, '⚠ MISSING')                            AS section_title_zh,
    COALESCE(bsc_en.title, '⚠ MISSING')                            AS section_title_en,
    CASE WHEN bsc_zh.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS chinese_status,
    CASE WHEN bsc_en.id IS NULL THEN 'MISSING' ELSE 'OK' END       AS english_status
FROM bibles.bible_sections bs
JOIN  bibles.bible_books bb
       ON bb.id = bs.bible_book_id
JOIN  bibles.bible_chapters bc
       ON bc.id = bs.bible_chapter_id
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
LEFT JOIN bibles.bible_section_contents bsc_zh
       ON bsc_zh.bible_section_id = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN bibles.bible_section_contents bsc_en
       ON bsc_en.bible_section_id = bs.id AND bsc_en.language = 'english'
WHERE bsc_zh.id IS NULL
   OR bsc_en.id IS NULL
ORDER BY bb.sort, bc.sort, bs.sort;


-- ── SECTION 4: Spec-driven structural check ──────────────────
-- Expect 0 rows.
-- Compares actual chapter count in the DB against the canonical spec value.
-- Chapter counts are identical for both CUV and BBE; only verse counts differ.
-- Any row returned here means the crawler did not create all expected chapters.

WITH spec (book_sort, expected_chapters, book_name_zh, book_name_en) AS (
    VALUES
        ( 1,  50, '創世記',           'Genesis'),
        ( 2,  40, '出埃及記',         'Exodus'),
        ( 3,  27, '利未記',           'Leviticus'),
        ( 4,  36, '民數記',           'Numbers'),
        ( 5,  34, '申命記',           'Deuteronomy'),
        ( 6,  24, '約書亞記',         'Joshua'),
        ( 7,  21, '士師記',           'Judges'),
        ( 8,   4, '路得記',           'Ruth'),
        ( 9,  31, '撒母耳記上',       '1 Samuel'),
        (10,  24, '撒母耳記下',       '2 Samuel'),
        (11,  22, '列王紀上',         '1 Kings'),
        (12,  25, '列王紀下',         '2 Kings'),
        (13,  29, '歷代志上',         '1 Chronicles'),
        (14,  36, '歷代志下',         '2 Chronicles'),
        (15,  10, '以斯拉記',         'Ezra'),
        (16,  13, '尼希米記',         'Nehemiah'),
        (17,  10, '以斯帖記',         'Esther'),
        (18,  42, '約伯記',           'Job'),
        (19, 150, '詩篇',             'Psalms'),
        (20,  31, '箴言',             'Proverbs'),
        (21,  12, '傳道書',           'Ecclesiastes'),
        (22,   8, '雅歌',             'Song of Solomon'),
        (23,  66, '以賽亞書',         'Isaiah'),
        (24,  52, '耶利米書',         'Jeremiah'),
        (25,   5, '耶利米哀歌',       'Lamentations'),
        (26,  48, '以西結書',         'Ezekiel'),
        (27,  12, '但以理書',         'Daniel'),
        (28,  14, '何西阿書',         'Hosea'),
        (29,   3, '約珥書',           'Joel'),
        (30,   9, '阿摩司書',         'Amos'),
        (31,   1, '俄巴底亞書',       'Obadiah'),
        (32,   4, '約拿書',           'Jonah'),
        (33,   7, '彌迦書',           'Micah'),
        (34,   3, '那鴻書',           'Nahum'),
        (35,   3, '哈巴谷書',         'Habakkuk'),
        (36,   3, '西番雅書',         'Zephaniah'),
        (37,   2, '哈該書',           'Haggai'),
        (38,  14, '撒迦利亞書',       'Zechariah'),
        (39,   4, '瑪拉基書',         'Malachi'),
        (40,  28, '馬太福音',         'Matthew'),
        (41,  16, '馬可福音',         'Mark'),
        (42,  24, '路加福音',         'Luke'),
        (43,  21, '約翰福音',         'John'),
        (44,  28, '使徒行傳',         'Acts'),
        (45,  16, '羅馬書',           'Romans'),
        (46,  16, '哥林多前書',       '1 Corinthians'),
        (47,  13, '哥林多後書',       '2 Corinthians'),
        (48,   6, '加拉太書',         'Galatians'),
        (49,   6, '以弗所書',         'Ephesians'),
        (50,   4, '腓立比書',         'Philippians'),
        (51,   4, '歌羅西書',         'Colossians'),
        (52,   5, '帖撒羅尼迦前書',   '1 Thessalonians'),
        (53,   3, '帖撒羅尼迦後書',   '2 Thessalonians'),
        (54,   6, '提摩太前書',       '1 Timothy'),
        (55,   4, '提摩太後書',       '2 Timothy'),
        (56,   3, '提多書',           'Titus'),
        (57,   1, '腓利門書',         'Philemon'),
        (58,  13, '希伯來書',         'Hebrews'),
        (59,   5, '雅各書',           'James'),
        (60,   5, '彼得前書',         '1 Peter'),
        (61,   3, '彼得後書',         '2 Peter'),
        (62,   5, '約翰一書',         '1 John'),
        (63,   1, '約翰二書',         '2 John'),
        (64,   1, '約翰三書',         '3 John'),
        (65,   1, '猶大書',           'Jude'),
        (66,  22, '啟示錄',           'Revelation')
)
SELECT
    s.book_sort,
    s.book_name_zh,
    s.book_name_en,
    s.expected_chapters,
    COUNT(bc.id)                                                    AS actual_chapters,
    s.expected_chapters - COUNT(bc.id)                              AS missing_chapters
FROM spec s
LEFT JOIN bibles.bible_books bb
       ON bb.sort = s.book_sort
LEFT JOIN bibles.bible_chapters bc
       ON bc.bible_book_id = bb.id
GROUP BY s.book_sort, s.book_name_zh, s.book_name_en, s.expected_chapters
HAVING s.expected_chapters - COUNT(bc.id) <> 0
ORDER BY s.book_sort;


-- ── SECTION 5: Versification-difference audit ─────────────────
-- Expect 0 rows after running cmd/repair.
-- Shows chapters where the number of ZH section_contents ≠ EN section_contents.
-- Before repair these differences are normal (e.g. Lev ch5 ZH=19 / EN=26).
-- After repair every section has both a real or placeholder content row,
-- so this query returns 0 rows on a fully repaired database.

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc_zh.title, '?')                                     AS book_name_zh,
    COALESCE(bbc_en.title, '?')                                     AS book_name_en,
    bc.sort                                                         AS chapter_sort,
    COUNT(DISTINCT bsc_zh.id)                                       AS zh_section_count,
    COUNT(DISTINCT bsc_en.id)                                       AS en_section_count,
    ABS(COUNT(DISTINCT bsc_zh.id) - COUNT(DISTINCT bsc_en.id))      AS diff
FROM bibles.bible_chapters bc
JOIN  bibles.bible_books bb
       ON bb.id = bc.bible_book_id
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
LEFT JOIN bibles.bible_sections bs
       ON bs.bible_chapter_id = bc.id
LEFT JOIN bibles.bible_section_contents bsc_zh
       ON bsc_zh.bible_section_id = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN bibles.bible_section_contents bsc_en
       ON bsc_en.bible_section_id = bs.id AND bsc_en.language = 'english'
GROUP BY bb.sort, bbc_zh.title, bbc_en.title, bc.sort
HAVING COUNT(DISTINCT bsc_zh.id) <> COUNT(DISTINCT bsc_en.id)
ORDER BY bb.sort, bc.sort;


-- ── SECTION 6: Chapter content viewer (bilingual) ────────────
-- Shows every verse in both languages for a given book + chapter.
--
-- ▶ CHANGE THESE TWO VALUES BEFORE RUNNING:
--     • book_name_param  → Chinese OR English book name
--     • chapter_num_param → chapter number (integer)
--
-- Examples:
--   '創世記' + 32   →  Genesis chapter 32
--   'Genesis' + 1   →  Genesis chapter 1
--   '詩篇'    + 119  →  Psalms chapter 119
--
-- Output columns:
--   verse_num  — 1-based verse number (same for both languages)
--   language   — 'chinese' | 'english'
--   title      — "第N節" or "verse N"
--   content    — the actual verse text
--   sub_title  — optional sub-heading (usually NULL)
--
-- Sort order:
--   1. language ASC  ('chinese' sorts before 'english' alphabetically,
--                     so no special CASE expression is needed)
--   2. verse_num ASC  (integer sort → 1, 2, 3 … 60, not "1","10","11","2")

WITH params AS (
    -- ── Set your query parameters here ───────────────────────
    SELECT
        '創世記'  AS book_name_param,   -- book name: Chinese OR English
        32        AS chapter_num_param   -- chapter number
    -- ─────────────────────────────────────────────────────────
),
target_book AS (
    -- Resolve the book ID from either a Chinese or English name.
    -- LIMIT 1 guards against accidental duplicates; both rows for a book
    -- map to the same bible_book_id so either row gives the correct ID.
    SELECT bbc.bible_book_id
    FROM   bibles.bible_book_contents bbc, params p
    WHERE  bbc.title = p.book_name_param
    LIMIT  1
)
SELECT
    p.book_name_param,
    p.chapter_num_param,
    bs.sort                                                         AS verse_num,
    bsc.language,
    bsc.title,
    bsc.content,
    bsc.sub_title
FROM       bibles.bible_section_contents  bsc
JOIN       bibles.bible_sections          bs  ON bs.id          = bsc.bible_section_id
JOIN       bibles.bible_chapters          bc  ON bc.id          = bs.bible_chapter_id
JOIN       bibles.bible_books             bb  ON bb.id          = bc.bible_book_id
JOIN       target_book                    tb  ON tb.bible_book_id = bb.id
CROSS JOIN params                         p
WHERE      bc.sort = p.chapter_num_param
ORDER BY
    bsc.language ASC,   -- 'chinese' < 'english' alphabetically → chinese rows first
    bs.sort      ASC;   -- integer verse number: 1, 2, 3, … (not string "1","10","2")


-- ── SECTION 7: Missing verse finder (per book + chapter) ─────────
-- Lists ONLY the (verse, language) pairs that have NO content row in the DB.
-- 0 rows = the chapter is fully complete in both languages.
--
-- ▶ CHANGE THESE TWO VALUES BEFORE RUNNING:
--     • book_name_param  → Chinese OR English book name
--                          e.g. '撒母耳記下'  or  '2 Samuel'
--     • chapter_num_param → chapter number (integer)
--
-- Output columns:
--   book_name_param   — the book name you supplied
--   chapter_num_param — the chapter number you supplied
--   verse_num         — 1-based verse number (bs.sort) of the missing content
--   language          — 'chinese' | 'english'  (which language is absent)
--   title             — always '⚠ MISSING' (content row does not exist in DB)
--
-- Interpretation:
--   Each row = one (verse, language) gap.
--   Common causes:
--     • YouVersion API returned 404 for this verse in that translation
--       (versification difference — e.g. NIV omits MAT.17.21, CSB omits 2SA.3.10)
--     • Crawler was interrupted before this verse was fetched

WITH params AS (
    -- ── Set your query parameters here ────────────────────────────
    SELECT
        '撒母耳記下'  AS book_name_param,   -- book name: Chinese OR English
        3             AS chapter_num_param   -- chapter number (integer)
    -- ──────────────────────────────────────────────────────────────
),
target_book AS (
    -- Resolve book ID from either Chinese or English name.
    -- LIMIT 1 guards against accidental duplicates in bible_book_contents.
    SELECT bbc.bible_book_id
    FROM   bibles.bible_book_contents bbc
    CROSS JOIN params p
    WHERE  bbc.title = p.book_name_param
    LIMIT  1
),
all_verse_lang_pairs AS (
    -- Full expected set: every verse in the chapter × both languages.
    -- This is what we SHOULD have content for.
    SELECT
        bs.id   AS section_id,
        bs.sort AS verse_num,
        lang.language
    FROM       bibles.bible_sections  bs
    JOIN       bibles.bible_chapters  bc  ON bc.id           = bs.bible_chapter_id
    JOIN       bibles.bible_books     bb  ON bb.id           = bc.bible_book_id
    JOIN       target_book            tb  ON tb.bible_book_id = bb.id
    CROSS JOIN (VALUES ('chinese'), ('english')) AS lang(language)
    CROSS JOIN params p
    WHERE      bc.sort = p.chapter_num_param
)
SELECT
    p.book_name_param,
    p.chapter_num_param,
    avlp.verse_num,
    avlp.language,
    '⚠ MISSING'::TEXT                  AS title  -- content row absent; cannot retrieve title
FROM       all_verse_lang_pairs        avlp
LEFT  JOIN bibles.bible_section_contents bsc
           ON  bsc.bible_section_id = avlp.section_id
           AND bsc.language         = avlp.language
CROSS JOIN params p
WHERE      bsc.id IS NULL             -- keep only the gaps (missing content rows)
ORDER BY
    avlp.verse_num ASC,               -- natural verse order (integer, not string)
    avlp.language  ASC;               -- 'chinese' before 'english' within same verse


-- ── SECTION 8: Post-import verse count summary ───────────────
-- Quick totals per language — run this first after every crawl + import.
-- Expected counts after a full bible.com (NIV + CUNP) crawl:
--   chinese: 31,092 total_section_contents | 31,092 unique sections
--   english: 31,103 total_section_contents | 31,103 unique sections
-- (EN > ZH because NIV includes 16 bracket-labeled textually-disputed
--  verses that CUV does not have versification gaps for.)
--
-- Output columns:
--   language               — 'chinese' | 'english'
--   total_section_contents — total rows in bible_section_contents
--   unique_sections        — distinct bibles.bible_sections rows covered
--   unique_chapters        — distinct chapters covered
--   unique_books           — should always be 66

SELECT
    bsc.language,
    COUNT(*)                                                        AS total_section_contents,
    COUNT(DISTINCT bs.id)                                           AS unique_sections,
    COUNT(DISTINCT bc.id)                                           AS unique_chapters,
    COUNT(DISTINCT bb.id)                                           AS unique_books
FROM bibles.bible_section_contents bsc
JOIN bibles.bible_sections  bs ON bs.id = bsc.bible_section_id
JOIN bibles.bible_chapters  bc ON bc.id = bs.bible_chapter_id
JOIN bibles.bible_books     bb ON bb.id = bc.bible_book_id
GROUP BY bsc.language
ORDER BY bsc.language;


-- ── SECTION 9: Empty content guard ───────────────────────────
-- Expect 0 rows.
-- bible_section_contents.content must never be NULL or blank.
-- Before the cross-reference resolution fix (cmd/biblecom-crawler),
-- this query would return 16 English rows for NIV bracket verses
-- (e.g. Matthew 17:21, Mark 9:44).  All rows here indicate an
-- import error that must be investigated.
--
-- Output columns:
--   book_sort / book_name_zh / book_name_en — identifies the book
--   chapter_sort / verse_sort               — location of the empty verse
--   language                                — 'chinese' | 'english'
--   content_preview                         — shows NULL or whitespace-only text

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc_zh.title, '⚠ 缺書名')                             AS book_name_zh,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS book_name_en,
    bc.sort                                                         AS chapter_sort,
    bs.sort                                                         AS verse_sort,
    bsc.language,
    COALESCE(bsc.content, 'NULL')                                   AS content_preview
FROM bibles.bible_section_contents bsc
JOIN  bibles.bible_sections  bs  ON bs.id  = bsc.bible_section_id
JOIN  bibles.bible_chapters  bc  ON bc.id  = bs.bible_chapter_id
JOIN  bibles.bible_books     bb  ON bb.id  = bc.bible_book_id
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
WHERE TRIM(COALESCE(bsc.content, '')) = ''
ORDER BY bb.sort, bc.sort, bs.sort, bsc.language;


-- ── SECTION 10: Bracket-verse spot-check (NIV) ───────────────
-- Verifies the 16 textually-disputed NIV verses that were previously
-- stored as empty content (causing 16 WARN lines during import).
-- After the cross-reference resolution fix:
--   • 7 verses resolved via cross-ref  → content copied from referenced verse
--   • 9 verses resolved via note text  → footnote body text stored as content
-- All english_status cells must show 'OK'.  Any '⚠ EMPTY' means the
-- bracket-verse fix did not propagate through correctly.
--
-- Resolution method column values:
--   cross-ref  — content sourced from another verse (cross_ref field in JSON)
--   note-text  — content is the footnote body text (fallback when no cross-ref)

WITH bracket_verses (book_sort, chapter_sort, verse_sort, resolution_method, cross_ref_source) AS (
    VALUES
        -- 7 resolved via cross-reference (content = another verse's prose)
        (40, 17, 21, 'cross-ref',  'MRK.9.29'),
        (40, 18, 11, 'cross-ref',  'LUK.19.10'),
        (40, 23, 14, 'cross-ref',  'MRK.12.40'),
        (41, 11, 26, 'cross-ref',  'MAT.6.15'),
        (41, 15, 28, 'cross-ref',  'LUK.22.37'),
        (42, 17, 36, 'cross-ref',  'MAT.24.40'),
        (42, 23, 17, 'cross-ref',  'MAT.27.15'),
        -- 9 using fallback note body text (no <span class="ref"> in footnote)
        (41,  7, 16, 'note-text',  ''),
        (41,  9, 44, 'note-text',  ''),
        (41,  9, 46, 'note-text',  ''),
        (43,  5,  4, 'note-text',  ''),
        (44,  8, 37, 'note-text',  ''),
        (44, 15, 34, 'note-text',  ''),
        (44, 24,  7, 'note-text',  ''),
        (44, 28, 29, 'note-text',  ''),
        (45, 16, 24, 'note-text',  '')
)
SELECT
    bv.book_sort,
    COALESCE(bbc_zh.title, '⚠ 缺書名')                             AS book_name_zh,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS book_name_en,
    bv.chapter_sort,
    bv.verse_sort,
    bv.resolution_method,
    NULLIF(bv.cross_ref_source, '')                                 AS cross_ref_source,
    LEFT(COALESCE(bsc_zh.content, '⚠ MISSING'), 60)                AS chinese_content,
    LEFT(COALESCE(bsc_en.content, '⚠ MISSING'), 60)                AS english_content,
    CASE
        WHEN bsc_en.content IS NULL OR TRIM(bsc_en.content) = ''
        THEN '⚠ EMPTY'
        ELSE 'OK'
    END                                                             AS english_status
FROM bracket_verses bv
JOIN  bibles.bible_books     bb  ON bb.sort               = bv.book_sort
JOIN  bibles.bible_chapters  bc  ON bc.bible_book_id      = bb.id
                                AND bc.sort               = bv.chapter_sort
JOIN  bibles.bible_sections  bs  ON bs.bible_chapter_id   = bc.id
                                AND bs.sort               = bv.verse_sort
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_book_contents bbc_en
       ON bbc_en.bible_book_id = bb.id AND bbc_en.language = 'english'
LEFT JOIN bibles.bible_section_contents bsc_zh
       ON bsc_zh.bible_section_id = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN bibles.bible_section_contents bsc_en
       ON bsc_en.bible_section_id = bs.id AND bsc_en.language = 'english'
ORDER BY bv.book_sort, bv.chapter_sort, bv.verse_sort;


-- ── SECTION 11: 中文空白小節查詢 ──────────────────────────────
-- 列出所有中文聖經內容缺失或空白的小節。
-- 兩種情況均涵蓋：
--   (A) bible_section_contents 資料列不存在（爬蟲未寫入）
--   (B) 資料列存在但 content 為 NULL 或純空白字元
--
-- 備註欄判斷邏輯（交叉比對英文內容）：
--   ✅ EN 有內容 → 版次差異：英文來源有此節，中文（CUV）省略，屬正常現象
--   ⚠ EN 同樣缺少 → 兩種語言均無資料，可能是爬蟲錯誤，需調查
--
-- 輸出欄位：
--   語言       — 固定為 '中文'
--   書本名稱    — 中文書名（e.g. 使徒行傳）
--   章節名稱    — 中文章節標題（e.g. 第8章）
--   小節編號    — 小節的 sort 整數（1-based 節號）
--   小節內容    — 診斷說明（缺少資料列 / 內容為 NULL / 內容為空白）
--   備註        — 交叉比對英文狀態，區分版次差異與爬蟲錯誤

SELECT
    '中文'                                                          AS 語言,
    COALESCE(bbc_zh.title, '⚠ 缺書名')                             AS 書本名稱,
    COALESCE(bcc_zh.title, '⚠ 缺章節名')                           AS 章節名稱,
    bs.sort                                                         AS 小節編號,
    CASE
        WHEN bsc_zh.id IS NULL              THEN '（缺少資料列）'
        WHEN bsc_zh.content IS NULL         THEN '（內容為 NULL）'
        ELSE                                     '（內容為空白）'
    END                                                             AS 小節內容,
    CASE
        WHEN bsc_en.id IS NOT NULL
         AND TRIM(COALESCE(bsc_en.content, '')) != ''
        THEN '✅ EN 有內容 → 版次差異（CUV 省略此節）'
        ELSE '⚠ EN 同樣缺少 → 兩語言均無資料，請調查'
    END                                                             AS 備註
FROM       bibles.bible_sections         bs
JOIN       bibles.bible_chapters         bc
           ON bc.id           = bs.bible_chapter_id
JOIN       bibles.bible_books            bb
           ON bb.id           = bc.bible_book_id
LEFT JOIN  bibles.bible_book_contents    bbc_zh
           ON bbc_zh.bible_book_id       = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN  bibles.bible_chapter_contents bcc_zh
           ON bcc_zh.bible_chapter_id    = bc.id AND bcc_zh.language = 'chinese'
LEFT JOIN  bibles.bible_section_contents bsc_zh
           ON bsc_zh.bible_section_id    = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN  bibles.bible_section_contents bsc_en
           ON bsc_en.bible_section_id    = bs.id AND bsc_en.language = 'english'
WHERE bsc_zh.id IS NULL
   OR TRIM(COALESCE(bsc_zh.content, '')) = ''
ORDER BY bb.sort, bc.sort, bs.sort;


-- ── SECTION 13: 中文空白小節詳細診斷查詢 ─────────────────────────
-- 目的
--   查詢所有中文聖經版本（language = 'chinese'）當中，content 缺失或為空白的小節。
--   與 SECTION 11 的差異在於：本查詢額外輸出書卷排序、章排序、section_id，
--   可直接用於 UPDATE / INSERT 修補作業，以及後續人工審查。
--
-- 涵蓋的兩種異常情況
--   (A) bible_section_contents 資料列不存在
--       → 問題類型顯示「❌ 完全缺少 contents 行」
--       → 爬蟲從未寫入此小節，需重爬或人工補值
--   (B) 資料列存在，但 content 欄位為 NULL 或純空白字元
--       → 問題類型顯示「⚠ content 欄位為 NULL」或「⚠ content 為空白字元」
--       → 屬於刻意留空的特殊小節（如：文本上有爭議之版次差異小節），
--         爬蟲依規則寫入空字串以保留 section 結構，未來可人工填入正確內容
--
-- 欄位說明
--   書卷排序   — 書卷的全域排序（1 = 創世記 … 66 = 啟示錄）
--   書本名稱   — 中文書名（e.g. 使徒行傳）；無 contents 行時顯示 '⚠ 缺書名'
--   章排序     — 章在書卷內的排序（1-based）
--   章節名稱   — 中文章節標題（e.g. 第 8 章）；無 contents 行時顯示 '⚠ 缺章節名'
--   節號       — 小節在章節內的排序（1-based 節號）
--   section_id — bible_sections.id（UUID），可直接用於 INSERT / UPDATE
--   問題類型   — 三種診斷結果（見上方說明）
--   content    — 實際儲存的內容（NULL 或空字串）；可用於確認問題性質
--
-- 使用方式
--   1. 執行此查詢確認缺失小節清單
--   2. 針對「❌ 完全缺少 contents 行」的 section_id，以下方範本補值：
--        INSERT INTO bibles.bible_section_contents
--          (bible_section_id, language, title, content)
--        VALUES ('<section_id>', 'chinese', '<第N節>', '')
--        ON CONFLICT DO NOTHING;
--   3. 確認補值後重新執行本查詢，結果應僅剩「⚠ content 為空白字元」

SELECT
    bb.sort                                                         AS 書卷排序,
    COALESCE(bbc.title, '⚠ 缺書名')                                AS 書本名稱,
    bc.sort                                                         AS 章排序,
    COALESCE(bcc.title, '⚠ 缺章節名')                              AS 章節名稱,
    bs.sort                                                         AS 節號,
    bs.id                                                           AS section_id,
    CASE
        WHEN bsc_zh.id IS NULL              THEN '❌ 完全缺少 contents 行'
        WHEN bsc_zh.content IS NULL         THEN '⚠ content 欄位為 NULL'
        ELSE                                     '⚠ content 為空白字元'
    END                                                             AS 問題類型,
    bsc_zh.content
FROM       bibles.bible_sections         bs
JOIN       bibles.bible_chapters         bc
           ON bc.id           = bs.bible_chapter_id
JOIN       bibles.bible_books            bb
           ON bb.id           = bc.bible_book_id
LEFT JOIN  bibles.bible_book_contents    bbc
           ON bbc.bible_book_id          = bb.id AND bbc.language = 'chinese'
LEFT JOIN  bibles.bible_chapter_contents bcc
           ON bcc.bible_chapter_id       = bc.id AND bcc.language = 'chinese'
LEFT JOIN  bibles.bible_section_contents bsc_zh
           ON bsc_zh.bible_section_id    = bs.id AND bsc_zh.language = 'chinese'
WHERE bsc_zh.id IS NULL
   OR TRIM(COALESCE(bsc_zh.content, '')) = ''
ORDER BY bb.sort, bc.sort, bs.sort;


-- ── SECTION 14: English empty verse detailed diagnostic query ─
-- Purpose
--   Query all English Bible version (language = 'english') verses whose
--   content is missing or empty. Compared with SECTION 12, this query adds
--   book_sort, chapter_sort, and section_id columns, making it suitable for
--   direct use in UPDATE / INSERT remediation workflows and manual review.
--
-- Two anomaly cases covered
--   (A) No bible_section_contents row exists for the (section, 'english') pair
--       → problem_type shows "❌ missing row entirely"
--       → The crawler never wrote this verse; re-crawl or insert manually
--   (B) The row exists but content is NULL or whitespace-only
--       → problem_type shows "⚠ content is NULL" or "⚠ content is blank"
--       → Intentionally empty for textually-disputed verses (versification
--         differences); the crawler writes "" to preserve the section structure;
--         may be filled in manually at a later date
--
-- Column descriptions
--   book_sort     — global canonical book order (1 = Genesis … 66 = Revelation)
--   book_name     — English book title (e.g. Acts); '⚠ MISSING' if no row
--   chapter_sort  — 1-based chapter index within the book
--   chapter_title — English chapter heading (e.g. Chapter 8); '⚠ MISSING' if absent
--   verse_number  — 1-based verse sort integer within the chapter
--   section_id    — bible_sections.id (UUID), usable directly in INSERT / UPDATE
--   problem_type  — one of three diagnostic labels (see above)
--   content       — actual stored value (NULL or empty string) for confirmation
--
-- How to use
--   1. Run this query to identify the list of missing / blank verses
--   2. For each section_id with "❌ missing row entirely", insert a placeholder:
--        INSERT INTO bibles.bible_section_contents
--          (bible_section_id, language, title, content)
--        VALUES ('<section_id>', 'english', 'Verse <N>', '')
--        ON CONFLICT DO NOTHING;
--   3. Re-run after remediation; only "⚠ content is blank" rows should remain

SELECT
    bb.sort                                                         AS book_sort,
    COALESCE(bbc.title, '⚠ MISSING')                               AS book_name,
    bc.sort                                                         AS chapter_sort,
    COALESCE(bcc.title, '⚠ MISSING')                               AS chapter_title,
    bs.sort                                                         AS verse_number,
    bs.id                                                           AS section_id,
    CASE
        WHEN bsc_en.id IS NULL              THEN '❌ missing row entirely'
        WHEN bsc_en.content IS NULL         THEN '⚠ content is NULL'
        ELSE                                     '⚠ content is blank'
    END                                                             AS problem_type,
    bsc_en.content
FROM       bibles.bible_sections         bs
JOIN       bibles.bible_chapters         bc
           ON bc.id           = bs.bible_chapter_id
JOIN       bibles.bible_books            bb
           ON bb.id           = bc.bible_book_id
LEFT JOIN  bibles.bible_book_contents    bbc
           ON bbc.bible_book_id          = bb.id AND bbc.language = 'english'
LEFT JOIN  bibles.bible_chapter_contents bcc
           ON bcc.bible_chapter_id       = bc.id AND bcc.language = 'english'
LEFT JOIN  bibles.bible_section_contents bsc_en
           ON bsc_en.bible_section_id    = bs.id AND bsc_en.language = 'english'
WHERE bsc_en.id IS NULL
   OR TRIM(COALESCE(bsc_en.content, '')) = ''
ORDER BY bb.sort, bc.sort, bs.sort;


-- ── SECTION 12: English empty verse query ────────────────────
-- Lists all English Bible verses whose content is missing or empty.
-- Two cases are covered:
--   (A) No bible_section_contents row exists for this (section, 'english') pair
--   (B) The row exists but content is NULL or whitespace-only
--
-- Note column (cross-checks Chinese content):
--   ✅ ZH has content → versification difference: Chinese source has this verse,
--                        English translation omits it — expected, not an error
--   ⚠ ZH also missing → both languages absent, possible crawl error, investigate
--
-- Output columns:
--   language      — always 'english'
--   book_name     — English book title (e.g. Revelation)
--   chapter_title — English chapter title (e.g. Chapter 12)
--   verse_number  — 1-based verse sort integer
--   verse_content — diagnostic note (missing row / NULL / blank)
--   note          — cross-check against Chinese to distinguish versification
--                   difference from crawl error

SELECT
    'english'                                                       AS language,
    COALESCE(bbc_en.title, '⚠ MISSING')                            AS book_name,
    COALESCE(bcc_en.title, '⚠ MISSING')                            AS chapter_title,
    bs.sort                                                         AS verse_number,
    CASE
        WHEN bsc_en.id IS NULL              THEN '(missing row)'
        WHEN bsc_en.content IS NULL         THEN '(content is NULL)'
        ELSE                                     '(content is blank)'
    END                                                             AS verse_content,
    CASE
        WHEN bsc_zh.id IS NOT NULL
         AND TRIM(COALESCE(bsc_zh.content, '')) != ''
        THEN '✅ ZH has content → versification difference (EN omits this verse)'
        ELSE '⚠ ZH also missing → both languages absent, investigate'
    END                                                             AS note
FROM       bibles.bible_sections         bs
JOIN       bibles.bible_chapters         bc
           ON bc.id           = bs.bible_chapter_id
JOIN       bibles.bible_books            bb
           ON bb.id           = bc.bible_book_id
LEFT JOIN  bibles.bible_book_contents    bbc_en
           ON bbc_en.bible_book_id       = bb.id AND bbc_en.language = 'english'
LEFT JOIN  bibles.bible_chapter_contents bcc_en
           ON bcc_en.bible_chapter_id    = bc.id AND bcc_en.language = 'english'
LEFT JOIN  bibles.bible_section_contents bsc_en
           ON bsc_en.bible_section_id    = bs.id AND bsc_en.language = 'english'
LEFT JOIN  bibles.bible_section_contents bsc_zh
           ON bsc_zh.bible_section_id    = bs.id AND bsc_zh.language = 'chinese'
WHERE bsc_en.id IS NULL
   OR TRIM(COALESCE(bsc_en.content, '')) = ''
ORDER BY bb.sort, bc.sort, bs.sort;
