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
	"gorm.io/gorm"
)

type AIChatHandler struct {
	DB  *gorm.DB
	Cfg *config.Config
}

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

	aiResponse := h.callAIChat(userID, req.Message, previousMessages)

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

func (h *AIChatHandler) callAIChat(userID uint, userMessage string, previousMessages []models.AIMessage) string {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	systemPrompt := fmt.Sprintf(
		`Та "FinTrack" санхүүгийн зөвлөгч AI юм. Хэрэглэгчийн санхүүгийн мэдээлэлд тулгуурлан Монгол хэлээр зөвлөгөө өгнө.

Хэрэглэгчийн санхүүгийн мэдээлэл:
%s

Дүрэм:
- Монгол хэлээр хариулна
- Мөнгөн дүнг ₮ (төгрөг) тэмдэгтэйгээр харуулна
- Хэрэглэгчийн санхүүгийн мэдээлэлд тулгуурлан бодит зөвлөгөө өгнө
- Хэмнэлт, хөрөнгө оруулалт, төсөвлөлтийн талаар зөвлөнө
- Товч, ойлгомжтой хариулна
- Emoji ашиглаж болно`, h.buildFinancialContext(userID))

	history := make([]aiMessage, 0, len(previousMessages))
	for _, msg := range previousMessages {
		role := "user"
		if msg.Role != "user" {
			role = "assistant"
		}
		history = append(history, aiMessage{Role: role, Content: msg.Content})
	}

	resp, err := callAI(ctx, systemPrompt, history, userMessage)
	if err != nil {
		log.Printf("AI chat failed: %v", err)
		return formatAIError(err)
	}
	return resp
}

func (h *AIChatHandler) buildFinancialContext(userID uint) string {
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
	return result
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
	case strings.Contains(message, "rate limit"), strings.Contains(message, "429"), strings.Contains(message, "too many requests"):
		return "AI үйлчилгээний хүсэлтийн хязгаарт хүрсэн байна. Хэдэн секунд хүлээгээд дахин оролдоно уу."
	case strings.Contains(message, "deadline"), strings.Contains(message, "timeout"), strings.Contains(message, "connection"), strings.Contains(message, "network"), strings.Contains(message, "unavailable"), strings.Contains(message, "no such host"):
		return "AI үйлчилгээ рүү холбогдож чадсангүй. Интернет холболтоо шалгаарай."
	case strings.Contains(message, "status 5"):
		return "AI үйлчилгээ түр зуур ажиллахгүй байна. Хэдэн минутын дараа дахин оролдоно уу."
	default:
		return fmt.Sprintf("AI үйлчилгээ алдаа өглөө: %s", err.Error())
	}
}
