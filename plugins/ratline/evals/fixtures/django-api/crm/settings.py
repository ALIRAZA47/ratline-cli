import environ
env = environ.Env()
SECRET_KEY = env("SECRET_KEY")
DEBUG = False
ALLOWED_HOSTS = ["api.acme.example"]
DATABASES = {"default": env.db("DATABASE_URL")}
STATIC_URL = "/static/"
STATIC_ROOT = "staticfiles"
INSTALLED_APPS = ["django.contrib.admin", "django.contrib.auth", "django.contrib.contenttypes", "django.contrib.sessions", "django.contrib.staticfiles", "accounts"]
ROOT_URLCONF = "crm.urls"
WSGI_APPLICATION = "crm.wsgi.application"
