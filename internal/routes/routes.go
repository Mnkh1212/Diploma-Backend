package routes

import (
	"fintrack-backend/internal/config"
	"fintrack-backend/internal/handlers"
	"fintrack-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func Setup(r *gin.Engine, db *gorm.DB, cfg *config.Config) {
	r.Use(middleware.CORS())

	authHandler := handlers.NewAuthHandler(db, cfg)
	txHandler := handlers.NewTransactionHandler(db)
	dashHandler := handlers.NewDashboardHandler(db)
	budgetHandler := handlers.NewBudgetHandler(db)
	aiHandler := handlers.NewAIChatHandler(db, cfg)
	accountHandler := handlers.NewAccountHandler(db)
	scheduledHandler := handlers.NewScheduledPaymentHandler(db)
	activityHandler := handlers.NewActivityLogHandler(db)
	notifHandler := handlers.NewNotificationHandler(db)
	importHandler := handlers.NewImportHandler(db, cfg)
	analysisHandler := handlers.NewAIAnalysisHandler(db, cfg)

	// Public routes
	api := r.Group("/api/v1")
	{
		api.POST("/auth/register", authHandler.Register)
		api.POST("/auth/login", authHandler.Login)
		api.GET("/auth/social/providers", authHandler.GetSocialProviders)
		api.POST("/auth/social/login", authHandler.SocialLogin)
	}

	// Protected routes
	protected := api.Group("")
	protected.Use(middleware.AuthMiddleware(cfg))
	{
		// Profile
		protected.GET("/profile", authHandler.GetProfile)
		protected.PUT("/profile", authHandler.UpdateProfile)
		protected.PUT("/profile/password", authHandler.ChangePassword)
		protected.POST("/profile/avatar", authHandler.UploadAvatar)

		// Dashboard
		protected.GET("/dashboard", dashHandler.GetDashboard)
		protected.GET("/expenses/summary", dashHandler.GetExpensesSummary)
		protected.GET("/statistics", dashHandler.GetStatistics)

		// Transactions
		protected.POST("/transactions", txHandler.Create)
		protected.GET("/transactions", txHandler.List)
		protected.GET("/transactions/:id", txHandler.Get)
		protected.DELETE("/transactions/:id", txHandler.Delete)

		// Budgets
		protected.POST("/budgets", budgetHandler.Create)
		protected.GET("/budgets", budgetHandler.List)
		protected.PUT("/budgets/:id", budgetHandler.Update)
		protected.DELETE("/budgets/:id", budgetHandler.Delete)

		// Accounts
		protected.POST("/accounts", accountHandler.Create)
		protected.GET("/accounts", accountHandler.List)
		protected.PUT("/accounts/:id", accountHandler.Update)
		protected.DELETE("/accounts/:id", accountHandler.Delete)

		// Categories
		protected.GET("/categories", accountHandler.GetCategories)

		// Scheduled Payments
		protected.POST("/scheduled-payments", scheduledHandler.Create)
		protected.GET("/scheduled-payments", scheduledHandler.List)
		protected.DELETE("/scheduled-payments/:id", scheduledHandler.Delete)
		protected.PUT("/scheduled-payments/:id/toggle", scheduledHandler.Toggle)

		// AI Chat
		protected.POST("/ai/chats", aiHandler.CreateChat)
		protected.GET("/ai/chats", aiHandler.ListChats)
		protected.GET("/ai/chats/:id", aiHandler.GetChat)
		protected.POST("/ai/chat", aiHandler.SendMessage)
		protected.DELETE("/ai/chats/:id", aiHandler.DeleteChat)

		// Activity Logs
		protected.GET("/activity-logs", activityHandler.List)
		protected.GET("/activity-logs/summary", activityHandler.Summary)

		// Notifications
		protected.POST("/push-token", notifHandler.SavePushToken)
		protected.GET("/notifications", notifHandler.ListNotifications)
		protected.PUT("/notifications/:id/read", notifHandler.MarkRead)
		protected.PUT("/notifications/read-all", notifHandler.MarkAllRead)

		// Import (хуучин — text-only AI summary)
		protected.POST("/import/statement", importHandler.ImportStatement)

		// AI Analysis (шинэ — structured JSON: balance, income, expense, transactions, charts)
		protected.POST("/ai/analysis", analysisHandler.AnalyzeStatement)
		protected.GET("/ai/analyses", analysisHandler.ListAnalyses)
		protected.GET("/ai/analyses/:id", analysisHandler.GetAnalysis)
		protected.DELETE("/ai/analyses/:id", analysisHandler.DeleteAnalysis)
	}
}
