package handlers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

type AIAnalysisHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

func NewAIAnalysisHandler(db *gorm.DB, cfg *config.Config) *AIAnalysisHandler {
	return &AIAnalysisHandler{DB: db, Cfg: cfg}
}

// AnalyzeStatement - банкны хуулга оруулмагц structured JSON буцаана.
//
// Process:
//  1. Файлыг хадгална.
//  2. Python parser байгаа бол түүн рүү файлыг proxy-лж parse-лна. Үгүй бол Go-н fallback.
//  3. Parsed-аас ангилалын нэгтгэл гаргана.
//  4. AI key байгаа бол Gemini-р хүний-уншигдах summary + recommendation үүсгэнэ.
//  5. AIAnalysis-ийг DB-д хадгалаад structured response буцаана.
func (h *AIAnalysisHandler) AnalyzeStatement(c *gin.Context) {
	userID := c.GetUint("user_id")

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Файл оруулна уу"})
		return
	}
	defer file.Close()

	dir := "./uploads/statements"
	if err := os.MkdirAll(dir, 0755); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Upload хавтас үүсгэж чадсангүй"})
		return
	}
	ext := strings.ToLower(filepath.Ext(header.Filename))
	if ext != ".pdf" && ext != ".xlsx" && ext != ".xls" && ext != ".csv" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Зөвхөн PDF, Excel, CSV дэмжинэ"})
		return
	}
	savedName := fmt.Sprintf("statement_%d_%d%s", userID, time.Now().Unix(), ext)
	savedPath := filepath.Join(dir, savedName)

	out, err := os.Create(savedPath)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Файл хадгалж чадсангүй"})
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Файл хуулж чадсангүй"})
		return
	}
	out.Close()

	// 1. Parsed мэдээлэл — Python service эсвэл Go fallback
	parsed, parseErr := h.parseStatement(savedPath, header.Filename)
	if parseErr != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": parseErr.Error()})
		return
	}

	// 2. Бэлэн категори байхгүй бол гүйлгээнээс үүсгэнэ
	if len(parsed.CategoryBreakdown) == 0 {
		parsed.CategoryBreakdown = computeCategoryBreakdown(parsed.Transactions)
	}

	// 3. AI summary + recommendation
	summary, recommendations := h.generateAIInsights(userID, parsed)

	// 4. DB-д хадгална
	catsJSON, _ := json.Marshal(parsed.CategoryBreakdown)
	txsJSON, _ := json.Marshal(parsed.Transactions)
	recsJSON, _ := json.Marshal(recommendations)

	record := models.AIAnalysis{
		UserID:              userID,
		Filename:            header.Filename,
		BankName:            parsed.BankName,
		OpeningBalance:      parsed.OpeningBalance,
		ClosingBalance:      parsed.ClosingBalance,
		TotalIncome:         parsed.TotalIncome,
		TotalExpenses:       parsed.TotalExpenses,
		TransactionCount:    len(parsed.Transactions),
		PeriodStart:         parsed.PeriodStart,
		PeriodEnd:           parsed.PeriodEnd,
		CategoriesJSON:      string(catsJSON),
		TransactionsJSON:    string(txsJSON),
		RecommendationsJSON: string(recsJSON),
		AISummary:           summary,
	}
	if err := h.DB.Create(&record).Error; err != nil {
		log.Printf("ai_analysis save failed: %v", err)
	}

	// Анализ хийсэн гүйлгээнүүдийг бодит Transaction record болгож хадгална.
	// Ингэснээр Dashboard, Гүйлгээ, Аналитик-д бүгд харагдана.
	// Хэрэв ?import=false бол алгасна.
	if c.DefaultQuery("import", "true") != "false" {
		imported, err := h.importParsedAsTransactions(userID, parsed)
		if err != nil {
			log.Printf("ai_analysis: import transactions failed: %v", err)
		} else {
			log.Printf("ai_analysis: imported %d transactions", imported)
		}
	}

	LogActivity(h.DB, userID, "ai_analysis", "ai_analysis", record.ID, header.Filename, "success", c.ClientIP())

	c.JSON(http.StatusOK, models.AIAnalysisResponse{
		ID:               record.ID,
		Filename:         header.Filename,
		BankName:         parsed.BankName,
		OpeningBalance:   parsed.OpeningBalance,
		ClosingBalance:   parsed.ClosingBalance,
		TotalIncome:      parsed.TotalIncome,
		TotalExpenses:    parsed.TotalExpenses,
		NetCashflow:      parsed.TotalIncome - parsed.TotalExpenses,
		TransactionCount: len(parsed.Transactions),
		PeriodStart:      parsed.PeriodStart,
		PeriodEnd:        parsed.PeriodEnd,
		Transactions:     parsed.Transactions,
		Categories:       parsed.CategoryBreakdown,
		Recommendations:  recommendations,
		AISummary:        summary,
		CreatedAt:        record.CreatedAt,
	})
}

// ListAnalyses - хэрэглэгчийн өмнөх анализуудын жагсаалт
func (h *AIAnalysisHandler) ListAnalyses(c *gin.Context) {
	userID := c.GetUint("user_id")

	var rows []models.AIAnalysis
	h.DB.Where("user_id = ?", userID).
		Order("created_at DESC").
		Limit(50).
		Find(&rows)

	out := make([]models.AIAnalysisResponse, 0, len(rows))
	for _, r := range rows {
		out = append(out, hydrateAnalysis(r))
	}
	c.JSON(http.StatusOK, out)
}

// GetAnalysis - тодорхой анализын дэлгэрэнгүй
func (h *AIAnalysisHandler) GetAnalysis(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var row models.AIAnalysis
	if err := h.DB.Where("user_id = ?", userID).First(&row, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Анализ олдсонгүй"})
		return
	}
	c.JSON(http.StatusOK, hydrateAnalysis(row))
}

// DeleteAnalysis - устгах
func (h *AIAnalysisHandler) DeleteAnalysis(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	res := h.DB.Where("user_id = ? AND id = ?", userID, id).Delete(&models.AIAnalysis{})
	if res.Error != nil || res.RowsAffected == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "Анализ олдсонгүй"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "Устгагдлаа"})
}

func hydrateAnalysis(r models.AIAnalysis) models.AIAnalysisResponse {
	var cats []models.CategoryBreakdown
	var txs []models.ParsedTransaction
	var recs []string
	_ = json.Unmarshal([]byte(r.CategoriesJSON), &cats)
	_ = json.Unmarshal([]byte(r.TransactionsJSON), &txs)
	_ = json.Unmarshal([]byte(r.RecommendationsJSON), &recs)

	return models.AIAnalysisResponse{
		ID:               r.ID,
		Filename:         r.Filename,
		BankName:         r.BankName,
		OpeningBalance:   r.OpeningBalance,
		ClosingBalance:   r.ClosingBalance,
		TotalIncome:      r.TotalIncome,
		TotalExpenses:    r.TotalExpenses,
		NetCashflow:      r.TotalIncome - r.TotalExpenses,
		TransactionCount: r.TransactionCount,
		PeriodStart:      r.PeriodStart,
		PeriodEnd:        r.PeriodEnd,
		Transactions:     txs,
		Categories:       cats,
		Recommendations:  recs,
		AISummary:        r.AISummary,
		CreatedAt:        r.CreatedAt,
	}
}

// ===================== Statement parsing =====================

func (h *AIAnalysisHandler) parseStatement(path, originalName string) (*models.ParsedStatement, error) {
	// 1. Python parser-ийг урьтал ашиглана
	if h.Cfg.ParserServiceURL != "" {
		parsed, err := callPythonParser(h.Cfg.ParserServiceURL, path, originalName)
		if err == nil && parsed != nil && len(parsed.Transactions) > 0 {
			return parsed, nil
		}
		log.Printf("python parser failed (%v) — Go fallback ажиллана", err)
	}
	// 2. Go-н fallback parser
	return goFallbackParse(path)
}

func callPythonParser(baseURL, filePath, originalName string) (*models.ParsedStatement, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	part, err := writer.CreateFormFile("file", originalName)
	if err != nil {
		return nil, err
	}
	if _, err := io.Copy(part, f); err != nil {
		return nil, err
	}
	writer.Close()

	url := strings.TrimRight(baseURL, "/") + "/parse"
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("parser status %d: %s", resp.StatusCode, string(raw))
	}

	var parsed models.ParsedStatement
	if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

// goFallbackParse - Python service байхгүй үед Go-р хийдэг хялбар parser.
//
// Маш олон банкны format-ыг яг таг тааруулах боломжгүй учраас зөвхөн дараахийг
// гаргаж авна:
//   - Файл доторх тоонуудыг скан хийн орлого/зарлагыг тооцоолох
//   - Хуулга PDF/Excel/CSV-ээс мөр уншиж "+" / "-" эсвэл хоёр баганаас тоог авах
func goFallbackParse(path string) (*models.ParsedStatement, error) {
	ext := strings.ToLower(filepath.Ext(path))
	var rawText string
	switch ext {
	case ".xlsx", ".xls":
		rawText = parseExcel(path)
	case ".pdf":
		rawText = parsePDF(path)
	case ".csv":
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		rawText = string(data)
	default:
		return nil, fmt.Errorf("дэмжигдээгүй файл")
	}
	if rawText == "" {
		return nil, fmt.Errorf("файлаас текст уншиж чадсангүй")
	}

	parsed := &models.ParsedStatement{
		BankName:     detectBankName(rawText),
		Transactions: []models.ParsedTransaction{},
	}

	// Mongolian bank format-ыг урьтал шалгана. ОРЛОГО / ЗАРЛАГА keyword-ууд ихтэй
	// бол Монгол banking parser ашиглана (Голомт г.м).
	upperAll := strings.ToUpper(rawText)
	if strings.Count(upperAll, "ОРЛОГО")+strings.Count(upperAll, "ЗАРЛАГА") >= 4 {
		mongolTxs := parseMongolianFormat(rawText)
		if len(mongolTxs) > 0 {
			parsed.Transactions = mongolTxs
			for _, tx := range mongolTxs {
				if tx.Type == "income" {
					parsed.TotalIncome += tx.Amount
				} else if tx.Type == "expense" {
					parsed.TotalExpenses += tx.Amount
				}
			}
		}
	}

	// Хэрэв Монгол parser ажиллахгүй бол generic line parser-ыг ашиглана.
	if len(parsed.Transactions) == 0 {
		for _, line := range strings.Split(rawText, "\n") {
			clean := strings.TrimSpace(line)
			if clean == "" {
				continue
			}
			tx, ok := parseLineToTransaction(clean)
			if !ok {
				continue
			}
			if tx.Amount < 100 {
				continue
			}
			if isOnlyDigitsOrPunct(tx.Description) {
				continue
			}
			parsed.Transactions = append(parsed.Transactions, tx)
			if tx.Type == "income" {
				parsed.TotalIncome += tx.Amount
			} else if tx.Type == "expense" {
				parsed.TotalExpenses += tx.Amount
			}
		}
	}

	// "НИЙТ ОРЛОГО / НИЙТ ЗАРЛАГА / Эхний/Эцсийн үлдэгдэл" гэх мэт summary мөрнөөс
	// override хийнэ — мөр-мөрийн нэгтгэлээс илүү найдвартай.
	summary := extractSummaryAmounts(rawText)
	if v, ok := summary["total_income"]; ok {
		parsed.TotalIncome = v
	}
	if v, ok := summary["total_expense"]; ok {
		parsed.TotalExpenses = v
	}
	if v, ok := summary["opening_balance"]; ok {
		parsed.OpeningBalance = v
	}
	if v, ok := summary["closing_balance"]; ok {
		parsed.ClosingBalance = v
	}

	// Period
	if len(parsed.Transactions) > 0 {
		parsed.PeriodStart = parsed.Transactions[0].Date
		parsed.PeriodEnd = parsed.Transactions[len(parsed.Transactions)-1].Date
	}

	return parsed, nil
}

// parseMongolianFormat - Голомт г.м Монгол банкны хэв format-д тааруулсан
// мөр-мөрийн parser.
//
//	2026-01-24                                                <- огнооны мөр
//	50,000.00   ОРЛОГО     ШИЛЖҮҮЛЭГ-...                     <- гүйлгээ
//	   500.00   ЗАРЛАГА    Данс хөтөлсний шимтгэл            <- гүйлгээ
//	50,000.00              ӨДРИЙН ОРЛОГО                     <- алгасна
//	   500.00              ӨДРИЙН ЗАРЛАГА                    <- алгасна
//	54,905.50              ӨДРИЙН ҮЛДЭГДЭЛ                   <- алгасна
//
// Дүн нь keyword-ээс өмнө байх онцлогтой.
func parseMongolianFormat(text string) []models.ParsedTransaction {
	var txs []models.ParsedTransaction
	currentDate := ""
	pendingIdx := -1 // multi-line description-ийн дагалдах мөрөнд ашиглана

	dailyLines := []string{"ӨДРИЙН ОРЛОГО", "ӨДРИЙН ЗАРЛАГА", "ӨДРИЙН ҮЛДЭГДЭЛ"}
	summaryLines := []string{"НИЙТ ОРЛОГО", "НИЙТ ЗАРЛАГА", "ЭХНИЙ ҮЛДЭГДЭЛ", "ЭЦСИЙН ҮЛДЭГДЭЛ"}

	for _, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" {
			pendingIdx = -1
			continue
		}

		// Огноо ганцаараа байгаа мөр уу?
		if d, ok := matchDateOnly(line); ok {
			currentDate = d
			pendingIdx = -1
			continue
		}

		upper := strings.ToUpper(line)

		// Өдрийн нэгтгэлийн мөр — алгасна
		if containsAny(upper, dailyLines) {
			pendingIdx = -1
			continue
		}
		// Нийт / эхний / эцсийн үлдэгдлийн мөр — алгасна (extractSummaryAmounts-р авна)
		if containsAny(upper, summaryLines) {
			pendingIdx = -1
			continue
		}

		// Гүйлгээний мөр — keyword-аар таних
		var txType, keyword string
		if strings.Contains(upper, "ЗАРЛАГА") {
			txType = "expense"
			keyword = "ЗАРЛАГА"
		} else if strings.Contains(upper, "ОРЛОГО") {
			txType = "income"
			keyword = "ОРЛОГО"
		}

		if txType != "" {
			kwIdx := strings.Index(upper, keyword)
			before := line[:kwIdx]
			after := strings.TrimSpace(line[kwIdx+len(keyword):])

			amt, ok := lastAmount(before)
			if !ok {
				pendingIdx = -1
				continue
			}
			desc := after
			if desc == "" {
				desc = "—"
			}
			tx := models.ParsedTransaction{
				Date:        currentDate,
				Description: desc,
				Amount:      amt,
				Type:        txType,
				Category:    classifyCategory(desc),
			}
			txs = append(txs, tx)
			pendingIdx = len(txs) - 1
			continue
		}

		// Үргэлжилсэн тайлбарын мөр — pending-тэй tx-ийн description-д залгана.
		// Тоогоор эхэлж байгаа мөрийг өөр гүйлгээ гэж үзэх боломжтой ч энд алгасна.
		if pendingIdx >= 0 && !looksLikeAmountFirst(line) {
			pending := &txs[pendingIdx]
			pending.Description = strings.TrimSpace(pending.Description + " " + line)
			pending.Category = classifyCategory(pending.Description)
		}
	}

	return txs
}

func matchDateOnly(line string) (string, bool) {
	tokens := strings.Fields(line)
	if len(tokens) != 1 {
		return "", false
	}
	t := tokens[0]
	if !looksLikeDate(t) {
		return "", false
	}
	// 2026-01-24 хэлбэрийг ISO форматт хөрвүүлэх
	for _, sep := range []string{"-", "/", "."} {
		parts := strings.Split(t, sep)
		if len(parts) == 3 {
			if len(parts[0]) == 4 {
				return fmt.Sprintf("%s-%s-%s", parts[0], pad2(parts[1]), pad2(parts[2])), true
			}
			if len(parts[2]) == 4 {
				return fmt.Sprintf("%s-%s-%s", parts[2], pad2(parts[1]), pad2(parts[0])), true
			}
		}
	}
	return t, true
}

func pad2(s string) string {
	if len(s) == 1 {
		return "0" + s
	}
	return s
}

func containsAny(s string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(s, n) {
			return true
		}
	}
	return false
}

func looksLikeAmountFirst(line string) bool {
	tokens := strings.Fields(line)
	if len(tokens) == 0 {
		return false
	}
	first := tokens[0]
	for _, r := range first {
		if (r >= '0' && r <= '9') || r == ',' || r == '.' || r == '-' {
			continue
		}
		return false
	}
	return true
}

func isOnlyDigitsOrPunct(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	for _, r := range s {
		if r >= '0' && r <= '9' {
			continue
		}
		if r == ' ' || r == '-' || r == '/' || r == '.' || r == ',' || r == ':' {
			continue
		}
		return false
	}
	return true
}

// extractSummaryAmounts - Монголын банкны хуулга дотроос "НИЙТ ОРЛОГО",
// "НИЙТ ЗАРЛАГА", "Эхний/Эцсийн үлдэгдэл" гэх мэт мөр хайж яг тоог олно.
//
// Зарим банк дүнг keyword-ийн өмнө бичдэг ("4,662,900.00 НИЙТ ОРЛОГО"),
// зарим нь араас. Тиймээс хоёр талыг шалгана.
func extractSummaryAmounts(raw string) map[string]float64 {
	out := map[string]float64{}
	if raw == "" {
		return out
	}
	low := strings.ToLower(raw)

	patterns := []struct {
		Key      string
		Keywords []string
	}{
		{"total_income", []string{"нийт орлого", "total income", "total credit", "нийт кредит"}},
		{"total_expense", []string{"нийт зарлага", "total expense", "total debit", "нийт дебет"}},
		{"opening_balance", []string{"эхний үлдэгдэл", "opening balance", "тайлант үеийн эхний"}},
		{"closing_balance", []string{"эцсийн үлдэгдэл", "closing balance", "үлдэгдэл эцэст"}},
	}

	for _, p := range patterns {
		for _, kw := range p.Keywords {
			idx := strings.Index(low, kw)
			if idx < 0 {
				continue
			}
			// Эхлээд keyword-ийн өмнөх 200 тэмдэгтээс хайна (Голомт стиль)
			start := idx - 200
			if start < 0 {
				start = 0
			}
			if v, ok := lastAmount(raw[start:idx]); ok {
				out[p.Key] = v
				break
			}
			// Олдохгүй бол ард нь
			tail := raw[idx+len(kw):]
			if len(tail) > 200 {
				tail = tail[:200]
			}
			if v, ok := firstAmount(tail); ok {
				out[p.Key] = v
				break
			}
		}
	}
	return out
}

// lastAmount - текстээс ХАМГИЙН СҮҮЛИЙН (≥100) том тоог буцаана.
func lastAmount(s string) (float64, bool) {
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '|' || r == ':' || r == '\r'
	})
	for i := len(tokens) - 1; i >= 0; i-- {
		raw := amountSep.Replace(tokens[i])
		raw = strings.TrimSuffix(raw, ".")
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		if absFloat(v) >= 100 {
			return absFloat(v), true
		}
	}
	return 0, false
}

// firstAmount - текстээс эхний "том" тоог олж float буцаана. 100-аас бага тоо
// (page номер, мөрийн дугаар гэх мэт) орхино.
func firstAmount(s string) (float64, bool) {
	tokens := strings.FieldsFunc(s, func(r rune) bool {
		return r == ' ' || r == '\n' || r == '\t' || r == '|' || r == ':' || r == '\r'
	})
	for _, t := range tokens {
		raw := amountSep.Replace(t)
		raw = strings.TrimSuffix(raw, ".")
		raw = strings.TrimPrefix(raw, ":")
		if raw == "" {
			continue
		}
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		if absFloat(v) >= 100 {
			return absFloat(v), true
		}
	}
	return 0, false
}

var amountSep = strings.NewReplacer(",", "", " ", "", "₮", "", "MNT", "", "mnt", "")

// parseLineToTransaction - "DATE  DESCRIPTION  +/- AMOUNT" гэх мэт хэвтэй мөрийг таних.
// Маш энгийн heuristic; banks-аас хамаарч файл format өөр байж болно.
func parseLineToTransaction(line string) (models.ParsedTransaction, bool) {
	tokens := strings.Fields(line)
	if len(tokens) < 2 {
		return models.ParsedTransaction{}, false
	}

	// Эхний token нь огноо байж болзошгүй
	date := ""
	for _, t := range tokens[:min(2, len(tokens))] {
		if looksLikeDate(t) {
			date = t
			break
		}
	}

	// Сүүлийн tokens-ийг ухраагаар үзэн анхны тоог олно
	for i := len(tokens) - 1; i >= 0; i-- {
		raw := amountSep.Replace(tokens[i])
		raw = strings.TrimSuffix(raw, ".")
		amt, err := strconv.ParseFloat(raw, 64)
		if err != nil || amt == 0 {
			continue
		}
		txType := "expense"
		if strings.HasPrefix(tokens[i], "+") || amt > 0 && strings.Contains(strings.ToLower(line), "credit") {
			txType = "income"
		}
		if strings.HasPrefix(tokens[i], "-") || strings.Contains(strings.ToLower(line), "debit") {
			txType = "expense"
		}
		amt = absFloat(amt)

		var desc string
		if date == "" {
			if i > 0 {
				desc = strings.Join(tokens[:i], " ")
			}
		} else if i > 1 {
			desc = strings.Join(tokens[1:i], " ")
		}
		desc = strings.TrimSpace(desc)
		if desc == "" {
			desc = "—"
		}
		return models.ParsedTransaction{
			Date:        date,
			Description: desc,
			Amount:      amt,
			Type:        txType,
			Category:    classifyCategory(desc),
		}, true
	}
	return models.ParsedTransaction{}, false
}

func looksLikeDate(s string) bool {
	if len(s) < 6 {
		return false
	}
	// 2025-01-15 / 2025/01/15 / 15.01.2025
	digits := 0
	for _, r := range s {
		if r >= '0' && r <= '9' {
			digits++
		}
	}
	return digits >= 6 && digits <= 10
}

func absFloat(f float64) float64 {
	if f < 0 {
		return -f
	}
	return f
}

func detectBankName(raw string) string {
	low := strings.ToLower(raw)
	switch {
	case strings.Contains(low, "khan bank"), strings.Contains(low, "хаан банк"):
		return "Khan Bank"
	case strings.Contains(low, "golomt"), strings.Contains(low, "голомт"):
		return "Golomt Bank"
	case strings.Contains(low, "tdb"), strings.Contains(low, "trade and development"), strings.Contains(low, "худалдаа хөгжил"):
		return "TDB"
	case strings.Contains(low, "khas"), strings.Contains(low, "хас банк"), strings.Contains(low, "xacbank"):
		return "Khas Bank"
	case strings.Contains(low, "state bank"), strings.Contains(low, "төрийн банк"):
		return "State Bank"
	default:
		return "Unknown Bank"
	}
}

// classifyCategory - description-аас дээр төрх ангилалд хуваарилна
func classifyCategory(desc string) string {
	low := strings.ToLower(desc)
	mapping := []struct {
		Keywords []string
		Category string
	}{
		{[]string{"хоол", "ресторан", "кафе", "food", "restaurant", "kfc", "mcdonald"}, "Хоол"},
		{[]string{"такси", "uber", "bolt", "taxi"}, "Такси"},
		{[]string{"шатахуун", "petrol", "gas", "shell", "petrovis"}, "Тээвэр"},
		{[]string{"emart", "номин", "nomin", "minii", "минии", "store", "shop", "дэлгүүр"}, "Дэлгүүр"},
		{[]string{"эрүүл мэнд", "эмнэлэг", "pharmacy", "эмийн сан", "hospital"}, "Эрүүл мэнд"},
		{[]string{"түрээс", "rent", "орон сууц"}, "Орон сууц"},
		{[]string{"unitel", "mobicom", "skytel", "gmobile", "интернет", "internet"}, "Интернет"},
		{[]string{"цалин", "salary", "tsalin"}, "Цалин"},
		{[]string{"шилжүүлэг", "transfer"}, "Шилжүүлэг"},
		{[]string{"netflix", "spotify", "youtube", "tiktok", "subscription"}, "Зугаа цэнгэл"},
		{[]string{"эрдэм", "сургууль", "school", "boloвсрол", "education"}, "Боловсрол"},
	}
	for _, m := range mapping {
		for _, k := range m.Keywords {
			if strings.Contains(low, strings.ToLower(k)) {
				return m.Category
			}
		}
	}
	return "Бусад"
}

func computeCategoryBreakdown(txs []models.ParsedTransaction) []models.CategoryBreakdown {
	totals := make(map[string]float64)
	counts := make(map[string]int)
	var grand float64
	for _, t := range txs {
		if t.Type != "expense" {
			continue
		}
		totals[t.Category] += t.Amount
		counts[t.Category]++
		grand += t.Amount
	}
	out := make([]models.CategoryBreakdown, 0, len(totals))
	for cat, amt := range totals {
		pct := 0.0
		if grand > 0 {
			pct = (amt / grand) * 100
		}
		out = append(out, models.CategoryBreakdown{
			Category:   cat,
			Amount:     amt,
			Percentage: pct,
			Count:      counts[cat],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Amount > out[j].Amount })
	return out
}

// ===================== AI insights =====================

func (h *AIAnalysisHandler) generateAIInsights(userID uint, parsed *models.ParsedStatement) (string, []string) {
	// Хэрэв AI key байхгүй бол rule-based fallback
	if h.Cfg.AIAPIKey == "" {
		return ruleBasedSummary(parsed), ruleBasedRecommendations(parsed)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	client, err := genai.NewClient(ctx, option.WithAPIKey(h.Cfg.AIAPIKey))
	if err != nil {
		log.Printf("gemini client init failed in analysis: %v", err)
		return ruleBasedSummary(parsed), ruleBasedRecommendations(parsed)
	}
	defer client.Close()

	model := client.GenerativeModel(h.Cfg.AIModel)
	model.SystemInstruction = genai.NewUserContent(genai.Text(
		`Та "FinTrack" санхүүгийн зөвлөгч AI юм. Монгол хэлээр товч, тодорхой хариулна.
Хэрэглэгчийн банкны хуулгад тулгуурлан JSON форматтай хариулна. Бусад текст, тайлбар бичихгүй.

Schema:
{
  "summary": "2-3 өгүүлбэртэй ерөнхий тойм",
  "recommendations": ["зөвлөмж 1", "зөвлөмж 2", "зөвлөмж 3", "зөвлөмж 4"]
}

Дүрэм:
- Мөнгөн дүнг ₮ тэмдэгтэй, монгол ёсоор бичнэ.
- Recommendations дэлгэрэнгүй, бодит тоонд тулгуурласан байна.
- Хариулт зөвхөн valid JSON. Markdown код блок ашиглахгүй.`))

	prompt := buildAnalysisPrompt(parsed)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Printf("gemini analysis failed: %v", err)
		return ruleBasedSummary(parsed), ruleBasedRecommendations(parsed)
	}

	if len(resp.Candidates) == 0 || len(resp.Candidates[0].Content.Parts) == 0 {
		return ruleBasedSummary(parsed), ruleBasedRecommendations(parsed)
	}

	raw := fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	raw = strings.TrimSpace(raw)
	// Gemini заримдаа ```json ... ``` буцаадаг
	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	var parsedAI struct {
		Summary         string   `json:"summary"`
		Recommendations []string `json:"recommendations"`
	}
	if err := json.Unmarshal([]byte(raw), &parsedAI); err != nil {
		log.Printf("gemini analysis JSON parse failed: %v\nraw=%s", err, raw)
		// JSON болохгүй бол raw-ийг summary болгоё
		return raw, ruleBasedRecommendations(parsed)
	}
	if parsedAI.Summary == "" {
		parsedAI.Summary = ruleBasedSummary(parsed)
	}
	if len(parsedAI.Recommendations) == 0 {
		parsedAI.Recommendations = ruleBasedRecommendations(parsed)
	}
	return parsedAI.Summary, parsedAI.Recommendations
}

func buildAnalysisPrompt(p *models.ParsedStatement) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Банкны хуулга шинжилгээ:\n")
	fmt.Fprintf(&b, "- Банк: %s\n", p.BankName)
	fmt.Fprintf(&b, "- Хугацаа: %s — %s\n", p.PeriodStart, p.PeriodEnd)
	fmt.Fprintf(&b, "- Эхлэлийн үлдэгдэл: %.0f₮\n", p.OpeningBalance)
	fmt.Fprintf(&b, "- Эцсийн үлдэгдэл: %.0f₮\n", p.ClosingBalance)
	fmt.Fprintf(&b, "- Нийт орлого: %.0f₮\n", p.TotalIncome)
	fmt.Fprintf(&b, "- Нийт зарлага: %.0f₮\n", p.TotalExpenses)
	fmt.Fprintf(&b, "- Гүйлгээний тоо: %d\n", len(p.Transactions))
	if len(p.CategoryBreakdown) > 0 {
		b.WriteString("\nЗарлагын ангилал (top 8):\n")
		for i, c := range p.CategoryBreakdown {
			if i >= 8 {
				break
			}
			fmt.Fprintf(&b, "  %s: %.0f₮ (%.1f%%, %d гүйлгээ)\n", c.Category, c.Amount, c.Percentage, c.Count)
		}
	}
	return b.String()
}

func ruleBasedSummary(p *models.ParsedStatement) string {
	net := p.TotalIncome - p.TotalExpenses
	state := "баланстай"
	if net > 0 {
		state = "хэмнэлттэй"
	} else if net < 0 {
		state = "зарцуулалт орлогоос их"
	}
	period := ""
	if p.PeriodStart != "" || p.PeriodEnd != "" {
		period = fmt.Sprintf(" (%s — %s)", p.PeriodStart, p.PeriodEnd)
	}
	return fmt.Sprintf(
		"%s банкны хуулга%s дээр %d гүйлгээ илэрсэн. Нийт орлого %.0f₮, зарлага %.0f₮, %s байна.",
		p.BankName, period, len(p.Transactions), p.TotalIncome, p.TotalExpenses, state,
	)
}

func ruleBasedRecommendations(p *models.ParsedStatement) []string {
	var recs []string
	net := p.TotalIncome - p.TotalExpenses
	if net < 0 {
		recs = append(recs, fmt.Sprintf("Энэ хугацаанд зарлага %0.f₮-өөр орлогоос хэтэрсэн. Шаардлагагүй захиалга, зугаа цэнгэлийн зардлыг хяна.", -net))
	} else if p.TotalIncome > 0 {
		rate := (net / p.TotalIncome) * 100
		recs = append(recs, fmt.Sprintf("Хэмнэлтийн хувь %.0f%%. Орлогынхоо 20%%-аас доош хэмнэвэл удирдамжаа сайжруулна.", rate))
	}
	if len(p.CategoryBreakdown) > 0 {
		top := p.CategoryBreakdown[0]
		recs = append(recs, fmt.Sprintf("\"%s\" ангилалд %.0f₮ зарцуулсан (зарлагын %.1f%%). Энэ ангилалд сарын төсөв тогтоох нь үр дүнтэй.", top.Category, top.Amount, top.Percentage))
	}
	if len(p.Transactions) > 30 {
		recs = append(recs, "Гүйлгээ их байна. Давтагдсан жижиг зарлагуудыг (subscriptions, кофе, такси) нэгтгэн хяна.")
	}
	if len(recs) == 0 {
		recs = []string{"Хуулга нь баланстай байна. Илүүдэл орлогыг хадгаламж эсвэл хөрөнгө оруулалт руу шилжүүлэх нь зүйтэй."}
	}
	return recs
}

// parseExcel/parsePDF re-used from import.go (same package)

// importParsedAsTransactions - parsed гүйлгээнүүдийг хэрэглэгчийн `transactions`
// table-д бодит record болгож хадгална. Эзэмшигч account-ыг автомат олно.
//
// Дараах нөхцөлүүдэд гүйлгээг алгасна:
//   - Amount < 100 (parser noise)
//   - p.Type income/expense биш
//   - Тохирох category олдоогүй ба default category-гүй
//
// Account.Balance-ийг (income - expense)-ээр шинэчилнэ.
func (h *AIAnalysisHandler) importParsedAsTransactions(userID uint, parsed *models.ParsedStatement) (int, error) {
	// 1. Зорилтот данс — анхны эзэмшигч данс. Үгүй бол шинэ "Bank (auto)" үүсгэнэ.
	var account models.Account
	err := h.DB.Where("user_id = ?", userID).Order("id ASC").First(&account).Error
	if err != nil {
		account = models.Account{
			UserID: userID,
			Name:   parsed.BankName,
			Type:   "bank",
			Icon:   "wallet",
			Color:  "#00C853",
		}
		if account.Name == "" || account.Name == "Unknown Bank" {
			account.Name = "Imported Bank"
		}
		if err := h.DB.Create(&account).Error; err != nil {
			return 0, fmt.Errorf("default account create failed: %w", err)
		}
	}

	// 2. Category map - нэрээр хайх.
	var cats []models.Category
	h.DB.Find(&cats)
	catByName := make(map[string]models.Category, len(cats))
	var defaultIncome, defaultExpense models.Category
	for _, c := range cats {
		catByName[strings.ToLower(strings.TrimSpace(c.Name))] = c
		if c.Type == "income" && defaultIncome.ID == 0 {
			defaultIncome = c
		}
		if c.Type == "expense" && defaultExpense.ID == 0 {
			defaultExpense = c
		}
	}

	// 3. Гүйлгээ бүрийг хадгална
	var inserted int
	var totalIn, totalOut float64
	const maxSaneAmount = 1_000_000_000 // 1 тэрбум (parser алдаатай дүн)
	for _, p := range parsed.Transactions {
		if p.Amount < 100 || p.Amount > maxSaneAmount {
			continue // parser-аас гарсан хэт жижиг эсвэл хэт том буруу дүн
		}
		if p.Type != "income" && p.Type != "expense" {
			continue
		}

		// Огноо
		date, err := time.Parse("2006-01-02", p.Date)
		if err != nil || date.IsZero() {
			date = time.Now()
		}

		// Category — нэрээр; үгүй бол default; type зөрвөл default-руу буцна
		cat, ok := catByName[strings.ToLower(strings.TrimSpace(p.Category))]
		if !ok || cat.Type != p.Type {
			if p.Type == "income" {
				cat = defaultIncome
			} else {
				cat = defaultExpense
			}
		}
		if cat.ID == 0 {
			continue // category байхгүй
		}

		desc := strings.TrimSpace(p.Description)
		if desc == "" {
			desc = "—"
		}

		tx := models.Transaction{
			UserID:      userID,
			AccountID:   account.ID,
			CategoryID:  cat.ID,
			Amount:      p.Amount,
			Type:        p.Type,
			Description: desc,
			Date:        date,
		}
		if err := h.DB.Create(&tx).Error; err != nil {
			log.Printf("import tx failed (%s %.0f): %v", p.Date, p.Amount, err)
			continue
		}
		inserted++
		if p.Type == "income" {
			totalIn += p.Amount
		} else {
			totalOut += p.Amount
		}
	}

	// 4. Account balance update — incremental
	if delta := totalIn - totalOut; delta != 0 {
		h.DB.Model(&models.Account{}).
			Where("id = ?", account.ID).
			Update("balance", gorm.Expr("balance + ?", delta))
	}

	return inserted, nil
}
