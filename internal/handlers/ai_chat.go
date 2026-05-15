package handlers

import (
	"context"
	"fmt"
	"log"
	"math"
	"net/http"
	"strings"
	"time"

	"fintrack-backend/internal/config"
	"fintrack-backend/internal/models"

	"github.com/gin-gonic/gin"
	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
	"gorm.io/gorm"
)

type AIChatHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

const aiSetupMessage = "AI API key тохируулаагүй байна. Backend орчинд `AI_API_KEY`, `GEMINI_API_KEY`, эсвэл `GOOGLE_API_KEY`-ийн аль нэгийг тохируулаад server-ээ restart хийгээрэй."

func NewAIChatHandler(db *gorm.DB, cfg *config.Config) *AIChatHandler {
	return &AIChatHandler{DB: db, Cfg: cfg}
}

func (h *AIChatHandler) CreateChat(c *gin.Context) {
	userID := c.GetUint("user_id")
	chat := models.AIChat{UserID: userID, Title: "New Chat"}
	if err := h.DB.Create(&chat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create chat"})
		return
	}
	c.JSON(http.StatusCreated, chat)
}

func (h *AIChatHandler) ListChats(c *gin.Context) {
	userID := c.GetUint("user_id")
	var chats []models.AIChat
	h.DB.Where("user_id = ?", userID).Order("updated_at DESC").Find(&chats)
	c.JSON(http.StatusOK, chats)
}

func (h *AIChatHandler) GetChat(c *gin.Context) {
	userID := c.GetUint("user_id")
	chatID := c.Param("id")
	var chat models.AIChat
	if err := h.DB.Preload("Messages", func(db *gorm.DB) *gorm.DB {
		return db.Order("created_at ASC")
	}).Where("user_id = ?", userID).First(&chat, chatID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}
	c.JSON(http.StatusOK, chat)
}

func (h *AIChatHandler) SendMessage(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req models.AIChatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	req.Message = strings.TrimSpace(req.Message)
	if req.Message == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Message is required"})
		return
	}

	var chat models.AIChat
	if req.ChatID > 0 {
		if err := h.DB.Where("user_id = ?", userID).First(&chat, req.ChatID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
			return
		}
	} else {
		chat = models.AIChat{UserID: userID, Title: truncateString(req.Message, 50)}
		if err := h.DB.Create(&chat).Error; err != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create chat"})
			return
		}
	}

	var previousMessages []models.AIMessage
	if err := h.DB.Where("chat_id = ?", chat.ID).Order("created_at ASC").Limit(20).Find(&previousMessages).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to load chat history"})
		return
	}

	userMsg := models.AIMessage{ChatID: chat.ID, Role: "user", Content: req.Message}
	if err := h.DB.Create(&userMsg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save user message"})
		return
	}

	var aiResponse string
	if h.Cfg.AIAPIKey != "" {
		aiResponse = h.callGemini(userID, req.AccountID, req.Message, previousMessages)
	} else {
		aiResponse = aiSetupMessage
	}

	aiMsg := models.AIMessage{ChatID: chat.ID, Role: "assistant", Content: aiResponse}
	if err := h.DB.Create(&aiMsg).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to save AI response"})
		return
	}

	var msgCount int64
	h.DB.Model(&models.AIMessage{}).Where("chat_id = ?", chat.ID).Count(&msgCount)
	if msgCount <= 2 {
		chat.Title = truncateString(req.Message, 50)
	}
	chat.UpdatedAt = time.Now()
	h.DB.Save(&chat)

	c.JSON(http.StatusOK, gin.H{"chat_id": chat.ID, "message": aiMsg})
	LogActivity(h.DB, userID, "ai_chat_message", "ai_chat", chat.ID, "", "success", c.ClientIP())
}

func (h *AIChatHandler) callGemini(userID, accountID uint, userMessage string, previousMessages []models.AIMessage) string {
	ctx := context.Background()

	financialContext, scopeNote := h.buildFinancialContext(userID, accountID)
	systemPrompt := fmt.Sprintf(
		`Та "FinTrack" санхүүгийн зөвлөгч AI юм. Хэрэглэгчийн санхүүгийн мэдээлэлд тулгуурлан Монгол хэлээр зөвлөгөө өгнө.

%s

Хэрэглэгчийн санхүүгийн мэдээлэл:
%s

Дүрэм:
- Монгол хэлээр хариулна
- Мөнгөн дүнг ₮ (төгрөг) тэмдэгтэйгээр харуулна
- Зөвхөн дээрх "%s"-ын мэдээлэлд тулгуурлан бодит зөвлөгөө өгнө
- Хэмнэлт, хөрөнгө оруулалт, төсөвлөлтийн талаар зөвлөнө
- Товч, ойлгомжтой хариулна
- Emoji ашиглаж болно`, scopeNote, financialContext, scopeNote)

	var geminiErr, orErr error

	// 1. Gemini direct — primary. Key байвал эхэлж туршина.
	if h.Cfg.AIAPIKey != "" {
		log.Printf("ai_chat: trying gemini (model=%s)", h.Cfg.AIModel)
		resp, err := h.tryGemini(ctx, systemPrompt, previousMessages, userMessage)
		if err == nil {
			return resp
		}
		geminiErr = err
		log.Printf("ai_chat: gemini failed: %v", err)
	}

	// 2. OpenRouter fallback — Gemini fail хийсэн үед.
	if h.Cfg.OpenRouterAPIKey != "" {
		log.Printf("ai_chat: trying openrouter (model=%s)", h.Cfg.OpenRouterModel)
		orHistory := convertHistoryToOR(previousMessages)
		resp, err := callOpenRouter(ctx, h.Cfg, systemPrompt, orHistory, userMessage)
		if err == nil {
			return resp
		}
		orErr = err
		log.Printf("ai_chat: openrouter failed: %v", err)
	}

	// Хоёулаа fail хийсэн — хэрэглэгчид яг ямар алдаа гарсныг харуулна
	if geminiErr == nil && orErr == nil {
		return aiSetupMessage
	}
	return formatMultiProviderError(geminiErr, orErr)
}

func (h *AIChatHandler) tryGemini(ctx context.Context, systemPrompt string, previousMessages []models.AIMessage, userMessage string) (string, error) {
	if h.Cfg.AIAPIKey == "" {
		return "", fmt.Errorf("gemini api key not configured")
	}
	client, err := genai.NewClient(ctx, option.WithAPIKey(h.Cfg.AIAPIKey))
	if err != nil {
		return "", err
	}
	defer client.Close()

	model := client.GenerativeModel(h.Cfg.AIModel)
	model.SystemInstruction = genai.NewUserContent(genai.Text(sanitizeUTF8(systemPrompt)))

	cs := model.StartChat()
	for _, msg := range previousMessages {
		role := "user"
		if msg.Role != "user" {
			role = "model"
		}
		cs.History = append(cs.History, &genai.Content{
			Role:  role,
			Parts: []genai.Part{genai.Text(sanitizeUTF8(msg.Content))},
		})
	}

	resp, err := cs.SendMessage(ctx, genai.Text(sanitizeUTF8(userMessage)))
	if err != nil {
		return "", err
	}
	if len(resp.Candidates) > 0 && len(resp.Candidates[0].Content.Parts) > 0 {
		return fmt.Sprintf("%v", resp.Candidates[0].Content.Parts[0]), nil
	}
	return "", fmt.Errorf("empty gemini response")
}

func convertHistoryToOR(prev []models.AIMessage) []orMessage {
	out := make([]orMessage, 0, len(prev))
	for _, msg := range prev {
		role := "user"
		if msg.Role != "user" {
			role = "assistant"
		}
		out = append(out, orMessage{Role: role, Content: msg.Content})
	}
	return out
}

// buildFinancialContext - AI prompt-д өгөх санхүүгийн мэдээлэл болон scope-г буцаана.
//
// Хэрэв accountID > 0 бол ЗӨВХӨН тухайн данстай холбоотой бүх хугацааны мэдээллийг
// (хуулга оруулсан хуучин огноо ч ороод) буцаана, ингэснээр AI зөвөлгөө тухайн
// банкинд "tailored" болж гарна. Үгүй бол одоогийн сарын аггрегат + бүх дансны нэгтгэл.
func (h *AIChatHandler) buildFinancialContext(userID, accountID uint) (string, string) {
	if accountID > 0 {
		return h.buildAccountContext(userID, accountID)
	}
	return h.buildAllAccountsContext(userID)
}

func (h *AIChatHandler) buildAccountContext(userID, accountID uint) (string, string) {
	var account models.Account
	if err := h.DB.Where("user_id = ? AND id = ?", userID, accountID).First(&account).Error; err != nil {
		// Account олдоогүй бол overall view руу буцна
		return h.buildAllAccountsContext(userID)
	}

	scope := fmt.Sprintf("Хамрах хүрээ: %s данс (бүх хугацаа)", account.Name)

	var income, expense float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND account_id = ? AND type = ?", userID, accountID, "income").
		Select("COALESCE(SUM(amount), 0)").Scan(&income)
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND account_id = ? AND type = ?", userID, accountID, "expense").
		Select("COALESCE(SUM(amount), 0)").Scan(&expense)

	var txCount int64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND account_id = ?", userID, accountID).Count(&txCount)

	type catExpense struct {
		Name  string
		Total float64
	}
	var topCategories []catExpense
	h.DB.Model(&models.Transaction{}).
		Select("categories.name, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.account_id = ? AND transactions.type = ?", userID, accountID, "expense").
		Group("categories.name").Order("total DESC").Limit(5).Scan(&topCategories)

	type recentTx struct {
		Date        time.Time
		Description string
		Amount      float64
		Type        string
	}
	var recents []recentTx
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND account_id = ?", userID, accountID).
		Order("date DESC, created_at DESC").Limit(8).
		Scan(&recents)

	savingsRate := 0.0
	if income > 0 {
		savingsRate = ((income - expense) / income) * 100
	}

	result := fmt.Sprintf("Данс: %s (%s)\n", account.Name, account.Type)
	result += fmt.Sprintf("Үлдэгдэл: %.0f₮\n", account.Balance)
	result += fmt.Sprintf("Нийт орлого: %.0f₮\n", income)
	result += fmt.Sprintf("Нийт зарлага: %.0f₮\n", expense)
	result += fmt.Sprintf("Цэвэр (орлого - зарлага): %.0f₮\n", income-expense)
	result += fmt.Sprintf("Хэмнэлтийн хувь: %.1f%%\n", savingsRate)
	result += fmt.Sprintf("Гүйлгээний тоо: %d\n", txCount)

	if len(topCategories) > 0 {
		result += "\nЭнэ дансны хамгийн их зарлагатай ангилал:\n"
		for i, cat := range topCategories {
			result += fmt.Sprintf("%d. %s: %.0f₮\n", i+1, cat.Name, cat.Total)
		}
	}

	if len(recents) > 0 {
		result += "\nСүүлийн гүйлгээнүүд:\n"
		for _, t := range recents {
			sign := "-"
			if t.Type == "income" {
				sign = "+"
			}
			result += fmt.Sprintf("- %s: %s%.0f₮ (%s)\n", t.Date.Format("2006-01-02"), sign, t.Amount, truncateString(t.Description, 50))
		}
	}

	return result, scope
}

func (h *AIChatHandler) buildAllAccountsContext(userID uint) (string, string) {
	scope := "Хамрах хүрээ: Бүх данс (энэ сар)"
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startOfLastMonth := startOfMonth.AddDate(0, -1, 0)

	var totalBalance float64
	h.DB.Model(&models.Account{}).Where("user_id = ?", userID).
		Select("COALESCE(SUM(balance), 0)").Scan(&totalBalance)

	var monthlyIncome, monthlyExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "income", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyIncome)
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyExpenses)

	var lastMonthExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startOfLastMonth, startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&lastMonthExpenses)

	type catExpense struct {
		Name  string
		Total float64
	}
	var topCategories []catExpense
	h.DB.Model(&models.Transaction{}).
		Select("categories.name, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ? AND transactions.date >= ?", userID, "expense", startOfMonth).
		Group("categories.name").Order("total DESC").Limit(5).Scan(&topCategories)

	var budgets []models.Budget
	h.DB.Preload("Category").
		Where("user_id = ? AND month = ? AND year = ?", userID, int(now.Month()), now.Year()).
		Find(&budgets)

	var accounts []models.Account
	h.DB.Where("user_id = ?", userID).Find(&accounts)

	savingsRate := 0.0
	if monthlyIncome > 0 {
		savingsRate = ((monthlyIncome - monthlyExpenses) / monthlyIncome) * 100
	}

	result := fmt.Sprintf("Нийт үлдэгдэл: %.0f₮\n", totalBalance)
	result += fmt.Sprintf("Энэ сарын орлого: %.0f₮\n", monthlyIncome)
	result += fmt.Sprintf("Энэ сарын зарлага: %.0f₮\n", monthlyExpenses)
	result += fmt.Sprintf("Өмнөх сарын зарлага: %.0f₮\n", lastMonthExpenses)
	result += fmt.Sprintf("Хэмнэлтийн хувь: %.1f%%\n", savingsRate)

	if len(accounts) > 0 {
		result += "\nДанснууд:\n"
		for _, a := range accounts {
			result += fmt.Sprintf("- %s (%s): %.0f₮\n", a.Name, a.Type, a.Balance)
		}
	}
	if len(topCategories) > 0 {
		result += "\nХамгийн их зарлагатай ангилал:\n"
		for i, cat := range topCategories {
			result += fmt.Sprintf("%d. %s: %.0f₮\n", i+1, cat.Name, cat.Total)
		}
	}
	if len(budgets) > 0 {
		result += "\nТөсөвлөлт:\n"
		for _, b := range budgets {
			catName := "Нийт"
			if b.Category.Name != "" {
				catName = b.Category.Name
			}
			pct := 0.0
			if b.Amount > 0 {
				pct = (b.Spent / b.Amount) * 100
			}
			result += fmt.Sprintf("- %s: %.0f₮ / %.0f₮ (%.0f%%)\n", catName, b.Spent, b.Amount, pct)
		}
	}
	return result, scope
}

func (h *AIChatHandler) DeleteChat(c *gin.Context) {
	userID := c.GetUint("user_id")
	chatID := c.Param("id")
	var chat models.AIChat
	if err := h.DB.Where("user_id = ?", userID).First(&chat, chatID).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
		return
	}
	h.DB.Where("chat_id = ?", chat.ID).Delete(&models.AIMessage{})
	h.DB.Delete(&chat)
	c.JSON(http.StatusOK, gin.H{"message": "Chat deleted"})
}

// Fallback: API key байхгүй эсвэл алдаа гарвал
func (h *AIChatHandler) generateFinancialAdvice(userID uint, question string) string {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startOfLastMonth := startOfMonth.AddDate(0, -1, 0)

	var totalBalance float64
	h.DB.Model(&models.Account{}).Where("user_id = ?", userID).
		Select("COALESCE(SUM(balance), 0)").Scan(&totalBalance)

	var monthlyIncome, monthlyExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "income", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyIncome)
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ?", userID, "expense", startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&monthlyExpenses)

	var lastMonthExpenses float64
	h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startOfLastMonth, startOfMonth).
		Select("COALESCE(SUM(amount), 0)").Scan(&lastMonthExpenses)

	type catExpense struct {
		Name  string
		Total float64
	}
	var topCategories []catExpense
	h.DB.Model(&models.Transaction{}).
		Select("categories.name, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ? AND transactions.date >= ?", userID, "expense", startOfMonth).
		Group("categories.name").Order("total DESC").Limit(5).Scan(&topCategories)

	var budgets []models.Budget
	h.DB.Preload("Category").
		Where("user_id = ? AND month = ? AND year = ?", userID, int(now.Month()), now.Year()).
		Find(&budgets)

	savingsRate := 0.0
	if monthlyIncome > 0 {
		savingsRate = ((monthlyIncome - monthlyExpenses) / monthlyIncome) * 100
	}
	expenseChange := 0.0
	if lastMonthExpenses > 0 {
		expenseChange = ((monthlyExpenses - lastMonthExpenses) / lastMonthExpenses) * 100
	}

	response := "📊 Таны санхүүгийн мэдээлэл:\n\n"
	response += fmt.Sprintf("💰 Нийт үлдэгдэл: %.0f₮\n", totalBalance)
	response += fmt.Sprintf("📈 Энэ сарын орлого: %.0f₮\n", monthlyIncome)
	response += fmt.Sprintf("📉 Энэ сарын зарлага: %.0f₮\n", monthlyExpenses)
	response += fmt.Sprintf("💵 Хэмнэлтийн хувь: %.1f%%\n\n", savingsRate)

	if expenseChange > 0 {
		response += fmt.Sprintf("⚠️ Зарлага өмнөх сараас %.1f%%-иар нэмэгдсэн.\n\n", expenseChange)
	} else if expenseChange < 0 {
		response += fmt.Sprintf("✅ Зарлага өмнөх сараас %.1f%%-иар буурсан!\n\n", math.Abs(expenseChange))
	}

	if len(topCategories) > 0 {
		response += "📋 Хамгийн их зарлагатай ангилал:\n"
		for i, cat := range topCategories {
			response += fmt.Sprintf("%d. %s: %.0f₮\n", i+1, cat.Name, cat.Total)
		}
		response += "\n"
	}
	
	for _, b := range budgets {
		if b.Amount > 0 && b.Spent/b.Amount > 0.8 {
			catName := "Нийт"
			if b.Category.Name != "" {
				catName = b.Category.Name
			}
			pct := (b.Spent / b.Amount) * 100
			if pct >= 100 {
				response += fmt.Sprintf("🚨 %s төсөв хэтэрсэн! %.0f₮ / %.0f₮ (%.0f%%)\n", catName, b.Spent, b.Amount, pct)
			} else {
				response += fmt.Sprintf("⚠️ %s төсөв дуусах дөхсөн: %.0f₮ / %.0f₮ (%.0f%%)\n", catName, b.Spent, b.Amount, pct)
			}
		}
	}

	response += "\n💡 Зөвлөмж:\n"
	if savingsRate < 20 {
		response += "- Орлогынхоо дор хаяж 20%%-ийг хэмнэхийг зорьж үзээрэй\n"
	}
	if len(topCategories) > 0 {
		response += fmt.Sprintf("- %s ангилалд төсөв тогтоож хяналт тавиарай\n", topCategories[0].Name)
	}
	if monthlyExpenses > monthlyIncome {
		response += "- ⚠️ Зарлага орлогоос хэтэрсэн байна. Шаардлагагүй зардлыг шалгаарай\n"
	} else {
		response += fmt.Sprintf("- %.0f₮ үлдсэн байна. Хөрөнгө оруулалтад зарцуулах боломжтой\n", monthlyIncome-monthlyExpenses)
	}
	return response
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

func formatAIError(err error) string {
	if err == nil {
		return "AI хариулт үүсгэх үед тодорхойгүй алдаа гарлаа."
	}

	message := strings.ToLower(err.Error())

	switch {
	case strings.Contains(message, "user location is not supported"), strings.Contains(message, "location is not supported"), strings.Contains(message, "not available in your country"):
		return "🌍 Google Gemini API нь Монголд хязгаарлагдсан байна.\n\n" +
			"Шийдэл (аль нэгийг сонгоно уу):\n\n" +
			"1️⃣ OpenRouter (хамгийн хялбар, үнэгүй):\n" +
			"   • https://openrouter.ai/keys ороод бүртгүүл\n" +
			"   • Шинэ key үүсгэх\n" +
			"   • Render → Environment → `OPENROUTER_API_KEY` тохируулах\n" +
			"   • Backend автоматаар Gemini-ээс OpenRouter руу шилжинэ\n\n" +
			"2️⃣ Шинэ Gemini key (VPN ашиглах):\n" +
			"   • US/EU VPN-ээр https://aistudio.google.com/app/apikey ороод шинэ key үүсгэх\n" +
			"   • Render → `AI_API_KEY` шинэчлэх"
	case strings.Contains(message, "leaked"):
		return "AI API key нь олон нийтэд илэрсэн тул Google автоматаар хаасан байна. Шинэ key үүсгэж, Render-ийн `AI_API_KEY` env var-ыг шинэчлэнэ үү."
	case strings.Contains(message, "api key"), strings.Contains(message, "permission denied"), strings.Contains(message, "unauthenticated"), strings.Contains(message, "authentication"):
		return "AI API key буруу эсвэл хүчингүй байна. Key-гээ шалгаад backend-ээ restart хийгээрэй."
	case strings.Contains(message, "quota"), strings.Contains(message, "rate limit"), strings.Contains(message, "resource_exhausted"):
		return "AI үйлчилгээний quota эсвэл rate limit дууссан байна. Дараа дахин оролдоно уу."
	case strings.Contains(message, "model"), strings.Contains(message, "not found"), strings.Contains(message, "unsupported"):
		return "AI model нэр буруу эсвэл энэ account дээр дэмжигдэхгүй байна. `AI_MODEL` тохиргоогоо шалгаарай."
	case strings.Contains(message, "deadline"), strings.Contains(message, "timeout"), strings.Contains(message, "connection"), strings.Contains(message, "network"), strings.Contains(message, "unavailable"):
		return "AI үйлчилгээ рүү холбогдож чадсангүй. Интернет холболт болон backend server-ийн сүлжээг шалгаарай."
	default:
		return fmt.Sprintf("AI үйлчилгээ алдаа өглөө: %s", err.Error())
	}
}
