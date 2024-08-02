FROM python:3.12-slim-bullseye
WORKDIR /code
COPY requirements.txt requirements.txt
RUN pip install -r requirements.txt
COPY transformer.py transformer.py
