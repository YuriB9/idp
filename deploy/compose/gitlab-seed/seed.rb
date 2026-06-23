# Минтит фиксированный root-PAT (scope=api) детерминированным значением фикстуры
# стенда. Запускается через `gitlab-rails runner /seed.rb` ВНУТРИ контейнера gitlab:
# password-grant (ROPC) в GitLab отключён, поэтому bootstrap токена выполняется
# через Rails, а не через OAuth/REST. Идемпотентно: повторный запуск не создаёт
# дубликат. Значение токена — фикстура стенда (не секрет прода), тем же значением
# аутентифицируется devinfra-worker (GITLAB_TOKEN).
token_value = ENV.fetch('SEED_TOKEN')
root = User.find_by_username('root') || User.find(1)

unless root.personal_access_tokens.find_by(name: 'devinfra-seed')
  pat = root.personal_access_tokens.create!(
    name: 'devinfra-seed',
    scopes: ['api'],
    expires_at: 300.days.from_now,
  )
  pat.set_token(token_value)
  pat.save!
  puts 'seed.rb: root-PAT выпущен'
else
  puts 'seed.rb: root-PAT уже существует (идемпотентно)'
end
