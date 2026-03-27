package handlers

import (
	"fmt"
	"math"
	"net/http"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type AIChatHandler struct {
	DB *gorm.DB
}

func NewAIChatHandler(db *gorm.DB) *AIChatHandler {
	return &AIChatHandler{DB: db}
}

func (h *AIChatHandler) CreateChat(c *gin.Context) {
	userID := c.GetUint("user_id")

	chat := models.AIChat{
		UserID: userID,
		Title:  "New Chat",
	}

	if err := h.DB.Create(&chat).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create chat"})
		return
	}

	c.JSON(http.StatusCreated, chat)
}

func (h *AIChatHandler) ListChats(c *gin.Context) {
	userID := c.GetUint("user_id")

	var chats []models.AIChat
	h.DB.Where("user_id = ?", userID).
		Order("updated_at DESC").
		Find(&chats)

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

	var chat models.AIChat
	if req.ChatID > 0 {
		if err := h.DB.Where("user_id = ?", userID).First(&chat, req.ChatID).Error; err != nil {
			c.JSON(http.StatusNotFound, gin.H{"error": "Chat not found"})
			return
		}
	} else {
		chat = models.AIChat{
			UserID: userID,
			Title:  truncateString(req.Message, 50),
		}
		h.DB.Create(&chat)
	}

	// Save user message
	userMsg := models.AIMessage{
		ChatID:  chat.ID,
		Role:    "user",
		Content: req.Message,
	}
	h.DB.Create(&userMsg)

	// Generate AI response based on user's financial data
	aiResponse := h.generateFinancialAdvice(userID, req.Message)

	// Save AI message
	aiMsg := models.AIMessage{
		ChatID:  chat.ID,
		Role:    "assistant",
		Content: aiResponse,
	}
	h.DB.Create(&aiMsg)

	// Update chat title if first message
	var msgCount int64
	h.DB.Model(&models.AIMessage{}).Where("chat_id = ?", chat.ID).Count(&msgCount)
	if msgCount <= 2 {
		chat.Title = truncateString(req.Message, 50)
		h.DB.Save(&chat)
	}

	c.JSON(http.StatusOK, gin.H{
		"chat_id":  chat.ID,
		"message":  aiMsg,
	})
	LogActivity(h.DB, userID, "ai_chat_message", "ai_chat", chat.ID, "", "success", c.ClientIP())
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

func (h *AIChatHandler) generateFinancialAdvice(userID uint, question string) string {
	now := time.Now()
	startOfMonth := time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, now.Location())
	startOfLastMonth := startOfMonth.AddDate(0, -1, 0)

	// Gather user's financial data
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

	// Top expense categories this month
	type catExpense struct {
		Name  string
		Total float64
	}
	var topCategories []catExpense
	h.DB.Model(&models.Transaction{}).
		Select("categories.name, SUM(transactions.amount) as total").
		Joins("JOIN categories ON categories.id = transactions.category_id").
		Where("transactions.user_id = ? AND transactions.type = ? AND transactions.date >= ?", userID, "expense", startOfMonth).
		Group("categories.name").
		Order("total DESC").
		Limit(5).
		Scan(&topCategories)

	// Budget status
	var budgets []models.Budget
	h.DB.Preload("Category").
		Where("user_id = ? AND month = ? AND year = ?", userID, int(now.Month()), now.Year()).
		Find(&budgets)

	// Build context-aware response
	savingsRate := 0.0
	if monthlyIncome > 0 {
		savingsRate = ((monthlyIncome - monthlyExpenses) / monthlyIncome) * 100
	}

	expenseChange := 0.0
	if lastMonthExpenses > 0 {
		expenseChange = ((monthlyExpenses - lastMonthExpenses) / lastMonthExpenses) * 100
	}

	response := fmt.Sprintf("Based on your financial data:\n\n")
	response += fmt.Sprintf("💰 **Current Balance:** $%.2f\n", totalBalance)
	response += fmt.Sprintf("📈 **This Month's Income:** $%.2f\n", monthlyIncome)
	response += fmt.Sprintf("📉 **This Month's Expenses:** $%.2f\n", monthlyExpenses)
	response += fmt.Sprintf("💵 **Savings Rate:** %.1f%%\n\n", savingsRate)

	if expenseChange > 0 {
		response += fmt.Sprintf("⚠️ Your expenses increased by %.1f%% compared to last month.\n\n", expenseChange)
	} else if expenseChange < 0 {
		response += fmt.Sprintf("✅ Great job! Your expenses decreased by %.1f%% compared to last month.\n\n", math.Abs(expenseChange))
	}

	if len(topCategories) > 0 {
		response += "**Top spending categories this month:**\n"
		for i, cat := range topCategories {
			response += fmt.Sprintf("%d. %s: $%.2f\n", i+1, cat.Name, cat.Total)
		}
		response += "\n"
	}

	// Budget warnings
	for _, b := range budgets {
		if b.Amount > 0 && b.Spent/b.Amount > 0.8 {
			catName := "Overall"
			if b.Category.Name != "" {
				catName = b.Category.Name
			}
			pct := (b.Spent / b.Amount) * 100
			if pct >= 100 {
				response += fmt.Sprintf("🚨 **Budget exceeded** for %s! Spent $%.2f of $%.2f budget (%.0f%%)\n", catName, b.Spent, b.Amount, pct)
			} else {
				response += fmt.Sprintf("⚠️ **Budget warning** for %s: $%.2f of $%.2f used (%.0f%%)\n", catName, b.Spent, b.Amount, pct)
			}
		}
	}

	// Add recommendations
	response += "\n**Recommendations:**\n"

	if savingsRate < 20 {
		response += "- Consider increasing your savings rate to at least 20%% of income\n"
	}
	if len(topCategories) > 0 {
		response += fmt.Sprintf("- Your highest expense category is %s. Consider setting a budget limit for it\n", topCategories[0].Name)
	}
	if monthlyExpenses > monthlyIncome {
		response += "- ⚠️ You're spending more than you earn this month. Review non-essential expenses\n"
	} else {
		response += fmt.Sprintf("- You have $%.2f remaining this month. Consider investing the surplus\n", monthlyIncome-monthlyExpenses)
	}

	return response
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
