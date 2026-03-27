package handlers

import (
	"net/http"
	"strconv"
	"time"

	"fintrack-backend/internal/models"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type TransactionHandler struct {
	DB *gorm.DB
}

func NewTransactionHandler(db *gorm.DB) *TransactionHandler {
	return &TransactionHandler{DB: db}
}

func (h *TransactionHandler) Create(c *gin.Context) {
	userID := c.GetUint("user_id")

	var req models.CreateTransactionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	date, err := time.Parse("2006-01-02", req.Date)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid date format, use YYYY-MM-DD"})
		return
	}

	tx := models.Transaction{
		UserID:      userID,
		AccountID:   req.AccountID,
		CategoryID:  req.CategoryID,
		Amount:      req.Amount,
		Type:        req.Type,
		Description: req.Description,
		Date:        date,
	}

	if err := h.DB.Create(&tx).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed to create transaction"})
		return
	}

	// Update account balance
	var account models.Account
	if err := h.DB.First(&account, req.AccountID).Error; err == nil {
		if req.Type == "income" {
			account.Balance += req.Amount
		} else {
			account.Balance -= req.Amount
		}
		h.DB.Save(&account)
	}

	// Update budget spent if expense
	if req.Type == "expense" {
		var budget models.Budget
		if err := h.DB.Where("user_id = ? AND category_id = ? AND month = ? AND year = ?",
			userID, req.CategoryID, int(date.Month()), date.Year()).First(&budget).Error; err == nil {
			budget.Spent += req.Amount
			h.DB.Save(&budget)
		}
	}

	h.DB.Preload("Category").Preload("Account").First(&tx, tx.ID)
	c.JSON(http.StatusCreated, tx)
}

func (h *TransactionHandler) List(c *gin.Context) {
	userID := c.GetUint("user_id")

	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	txType := c.Query("type")
	search := c.Query("search")

	offset := (page - 1) * limit

	query := h.DB.Where("user_id = ?", userID)

	if txType != "" {
		query = query.Where("type = ?", txType)
	}
	if search != "" {
		query = query.Where("description ILIKE ?", "%"+search+"%")
	}

	var total int64
	query.Model(&models.Transaction{}).Count(&total)

	var transactions []models.Transaction
	query.Preload("Category").Preload("Account").
		Order("date DESC, created_at DESC").
		Offset(offset).Limit(limit).
		Find(&transactions)

	c.JSON(http.StatusOK, gin.H{
		"data":  transactions,
		"total": total,
		"page":  page,
		"limit": limit,
	})
}

func (h *TransactionHandler) Get(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var tx models.Transaction
	if err := h.DB.Preload("Category").Preload("Account").
		Where("user_id = ?", userID).First(&tx, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}

	c.JSON(http.StatusOK, tx)
}

func (h *TransactionHandler) Delete(c *gin.Context) {
	userID := c.GetUint("user_id")
	id := c.Param("id")

	var tx models.Transaction
	if err := h.DB.Where("user_id = ?", userID).First(&tx, id).Error; err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "Transaction not found"})
		return
	}

	// Reverse balance change
	var account models.Account
	if err := h.DB.First(&account, tx.AccountID).Error; err == nil {
		if tx.Type == "income" {
			account.Balance -= tx.Amount
		} else {
			account.Balance += tx.Amount
		}
		h.DB.Save(&account)
	}

	h.DB.Delete(&tx)
	c.JSON(http.StatusOK, gin.H{"message": "Transaction deleted"})
}
