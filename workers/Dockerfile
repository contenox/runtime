FROM python:3.12-slim

WORKDIR /app

#COPY workers/requirements.txt .
#RUN pip install -r requirements.txt

COPY . .

CMD ["python", "start_worker.py"]
