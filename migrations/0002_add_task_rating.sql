-- Migration: Add task rating
-- Version: 0002
-- Created: 2025-01-15

-- Добавляем поле для голоса пользователя за свою задачу
ALTER TABLE tasks ADD COLUMN user_rating TEXT CHECK (user_rating IN ('upvote', 'downvote', NULL));

-- Добавляем индекс на user_rating для статистики
CREATE INDEX idx_tasks_user_rating ON tasks(user_rating);
