package handlers

import (
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

type ImportHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

func NewImportHandler(db *gorm.DB, cfg *config.Config) *ImportHandler {
	return &ImportHandler{DB: db, Cfg: cfg}
}

// ImportStatement - банкны хуулга upload хийж AI-аар анализ хийх
func (h *ImportHandler) ImportStatement(c *gin.Context) {
	userID := c.GetUint("user_id")

	file, header, err := c.Request.FormFile("file")
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Файл оруулна уу"})
		return
	}
	defer file.Close()

	// Файл хадгалах
	dir := "./uploads/statements"
	os.MkdirAll(dir, 0755)
	ext := strings.ToLower(filepath.Ext(header.Filename))
	filename := fmt.Sprintf("statement_%d_%d%s", userID, time.Now().Unix(), ext)
	path := filepath.Join(dir, filename)

	out, err := os.Create(path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Файл хадгалж чадсангүй"})
		return
	}
	defer out.Close()
	io.Copy(out, file)

	// Файлаас текст задлах
	var rawText string
	switch ext {
	case ".xlsx", ".xls":
		rawText = parseExcel(path)
	case ".pdf":
		rawText = parsePDF(path)
	case ".csv":
		data, _ := os.ReadFile(path)
		rawText = string(data)
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Зөвхөн PDF, Excel, CSV файл дэмжинэ"})
		return
	}

	if rawText == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Файлаас мэдээлэл уншиж чадсангүй"})
		return
	}

	// AI-аар анализ хийх
	analysis := h.analyzeWithAI(rawText, userID)

	c.JSON(http.StatusOK, gin.H{
		"message":  "Файл амжилттай импортлогдлоо",
		"filename": header.Filename,
		"analysis": analysis,
	})
}

func parseExcel(path string) string {
	f, err := excelize.OpenFile(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var result strings.Builder
	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		rows, err := f.GetRows(sheet)
		if err != nil {
			continue
		}
		for _, row := range rows {
			result.WriteString(strings.Join(row, " | "))
			result.WriteString("\n")
		}
	}
	return result.String()
}

func parsePDF(path string) string {
	f, r, err := pdf.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()

	var result strings.Builder
	totalPage := r.NumPage()
	for i := 1; i <= totalPage; i++ {
		p := r.Page(i)
		if p.V.IsNull() {
			continue
		}
		text, err := p.GetPlainText(nil)
		if err != nil {
			continue
		}
		result.WriteString(text)
		result.WriteString("\n")
	}
	return result.String()
}

func (h *ImportHandler) analyzeWithAI(rawText string, userID uint) string {
	if h.Cfg.AIAPIKey == "" {
		return h.basicAnalysis(rawText)
	}

	ctx := context.Background()
	client, err := genai.NewClient(ctx, option.WithAPIKey(h.Cfg.AIAPIKey))
	if err != nil {
		log.Printf("gemini client init failed in import: %v", err)
		return h.basicAnalysis(rawText)
	}
	defer client.Close()

	// Хэрэглэгчийн одоогийн санхүүгийн мэдээлэл
	var totalBalance float64
	h.DB.Model(&models.Account{}).Where("user_id = ?", userID).
		Select("COALESCE(SUM(balance), 0)").Scan(&totalBalance)

	model := client.GenerativeModel(h.Cfg.AIModel)
	model.SystemInstruction = genai.NewUserContent(genai.Text(
		`Та Монголын банкны гүйлгээний хуулга анализ хийж байна. Монгол хэлээр хариулна.

Дараах зүйлсийг тодорхойлно уу:
1. 📊 Нийт орлого болон зарлагын дүн
2. 📋 Зарлагын ангилал (хоол, тээвэр, дэлгүүр, шилжүүлэг гэх мэт)
3. 📈 Хамгийн их зарлага гарсан ангилал
4. ⚠️ Анхааруулга (хэт их зарлага, давтагдсан зарлага гэх мэт)
5. 💡 Хэмнэлтийн зөвлөмж (яг тоон дүнтэйгээр)
6. 🎯 Төсөвлөлтийн санал (ангилал тус бүрээр)

Мөнгөн дүнг ₮ тэмдэгтэйгээр, таслалтай бичнэ. Товч, тодорхой бай.`))

	// Текстийг хэт урт бол товчлох
	text := rawText
	if len(text) > 15000 {
		text = text[:15000] + "\n...(хасагдсан)"
	}

	prompt := fmt.Sprintf("Банкны гүйлгээний хуулга:\n\n%s\n\nХэрэглэгчийн одоогийн нийт үлдэгдэл: %.0f₮", text, totalBalance)

	resp, err := model.GenerateContent(ctx, genai.Text(prompt))
	if err != nil {
		log.Printf("gemini generate content failed in import for model %s: %v", h.Cfg.AIModel, err)
		return h.basicAnalysis(rawText)
	}

	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		return fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0])
	}
	return h.basicAnalysis(rawText)
}

func (h *ImportHandler) basicAnalysis(rawText string) string {
	lines := strings.Split(rawText, "\n")
	lineCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) != "" {
			lineCount++
		}
	}
	return fmt.Sprintf("📊 Файлаас %d мөр мэдээлэл уншигдлаа.\n\n💡 AI анализ хийхийн тулд backend орчинд `AI_API_KEY`, `GEMINI_API_KEY`, эсвэл `GOOGLE_API_KEY`-ийн аль нэгийг тохируулна уу.", lineCount)
}
