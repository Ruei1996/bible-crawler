-- ============================================================
-- Bible Data Validation Script
-- ============================================================
-- Validates the bibles schema against the canonical 66-book spec.
--
-- Structure:
--   SECTION 0 — Global summary (counts & missing at every level)
--   SECTION 1 — Books  missing Chinese or English title
--   SECTION 2 — Chapters missing Chinese or English title
--   SECTION 3 — Sections (verses) missing Chinese or English content
--   SECTION 4 — Spec-driven structural check (expected vs actual chapters)
--   SECTION 5 — Versification-difference audit (ZH vs EN section counts)
--   SECTION 6 — Chapter content viewer: query by book name + chapter number
--
-- A fully loaded and repaired database returns:
--   • 0 rows  in sections 1, 2, 3, 4, 5
--   • missing_chinese = 0 and missing_english = 0 in section 0
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
    bc.sort                                                         AS chapter_sort,
    COUNT(DISTINCT bsc_zh.id)                                       AS zh_section_count,
    COUNT(DISTINCT bsc_en.id)                                       AS en_section_count,
    ABS(COUNT(DISTINCT bsc_zh.id) - COUNT(DISTINCT bsc_en.id))      AS diff
FROM bibles.bible_chapters bc
JOIN  bibles.bible_books bb
       ON bb.id = bc.bible_book_id
LEFT JOIN bibles.bible_book_contents bbc_zh
       ON bbc_zh.bible_book_id = bb.id AND bbc_zh.language = 'chinese'
LEFT JOIN bibles.bible_sections bs
       ON bs.bible_chapter_id = bc.id
LEFT JOIN bibles.bible_section_contents bsc_zh
       ON bsc_zh.bible_section_id = bs.id AND bsc_zh.language = 'chinese'
LEFT JOIN bibles.bible_section_contents bsc_en
       ON bsc_en.bible_section_id = bs.id AND bsc_en.language = 'english'
GROUP BY bb.sort, bbc_zh.title, bc.sort
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