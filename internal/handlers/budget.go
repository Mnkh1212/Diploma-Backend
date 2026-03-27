package handlers

import (
	"net/http"
	"strconv"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type BudgetHandler struct {
	DB *gorm.DB
}

func NewBudgetHandler(db *gorm.DB) *BudgetHandler {
	return &BudgetHandler{DB: db}
}

func (h *BudgetHandler) Create(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req models.CreateBudgetRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// Check for existing budget
	var existing models.Budget
	query := h.DB.Where("user_id = ? AND month = ? AND year = ?", userID, req.Month, req.Year)
	if req.CategoryID > 0 {
		query = query.Where("category_id = ?", req.CategoryID)
	}
	if query.First(&existing).Error == nil {
		c.JSON(http.StatusConflict, gin.H{"error": "Budget already exists for this period"})
		return
	}

	budget := models.Budget{
		UserID:     userID,
		CategoryID: req.CategoryID,
		Amount:     req.Amount,
		Month:      req.Month,
		Year:       req.Year,
	}

	// Calculate already spent
	startDate := time.Date(req.Year, time.Month(req.Month), 1, 0, 0, 0, 0, time.UTC)
	endDate := startDate.AddDate(0, 1, 0)

	spentQuery := h.DB.Model(&models.Transaction{}).
		Where("user_id = ? AND type = ? AND date >= ? AND date < ?", userID, "expense", startDate, endDate)
	if req.CategoryID > 0 {
		spentQuery = spentQuery.Where("category_id = ?", req.CategoryID)
	}
	spentQuery.Select("COALESCE(SUM(amount), 0)").Scan(&budget.Spent)

	if err := h.DB.Create(&budget).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create budget"})
		return
	}

	h.DB.Preload("Category").First(&budget, budget.ID)
	c.JSON(http.StatusCreated, budget)
}

func (h *BudgetHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")
	now := time.Now()
	month, _ := strconv.Atoi(c.DefaultQuery("month", strconv.Itoa(int(now.Month()))))
	year, _ := strconv.Atoi(c.DefaultQuery("year", strconv.Itoa(now.Year())))

	var budgets []models.Budget
	h.DB.Preload("Category").
		Where("user_id = ? AND month = ? AND year = ?", userID, month, year).
		Find(&budgets)

	// Calculate total budget and spent
	var totalBudget, totalSpent float64
	for _, b := range budgets {
		totalBudget += b.Amount
		totalSpent += b.Spent
	}

	c.JSON(http.StatusOK, gin.H{
		"budgets":      budgets,
		"total_budget": totalBudget,
		"total_spent":  totalSpent,
		"month":        month,
		"year":         year,
	})
}

func (h *BudgetHandler) Update(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var budget models.Budget
	if err := h.DB.Where("user_id = ?", userID).First(&budget, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Budget not found"})
		return
	}

	var input struct {
		Amount float64 `json:"amount" binding:"required"`
	}
	if err := c.ShouldBindJSON(&input); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	budget.Amount = input.Amount
	h.DB.Save(&budget)

	h.DB.Preload("Category").First(&budget, budget.ID)
	c.JSON(http.StatusOK, budget)
}

func (h *BudgetHandler) Delete(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var budget models.Budget
	if err := h.DB.Where("user_id = ?", userID).First(&budget, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Budget not found"})
		return
	}

	h.DB.Delete(&budget)
	c.JSON(http.StatusOK, gin.H{"message": "Budget deleted"})
}
