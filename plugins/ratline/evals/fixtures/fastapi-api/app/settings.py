from pydantic_settings import BaseSettings
class Settings(BaseSettings):
    database_url: str          # postgres, hosted at Neon
    redis_url: str
    jwt_secret: str
    log_level: str = "info"
settings = Settings()
