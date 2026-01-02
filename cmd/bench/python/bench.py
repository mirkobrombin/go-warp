import time
import redis
import sys

PORT = 6381
HOST = "127.0.0.1"
TOTAL_OPS = 50000

def run():
    r = redis.Redis(host=HOST, port=PORT, db=0)
    
    print(f"Connecting to {HOST}:{PORT}...")
    try:
        r.set("warmup", "1")
        r.get("warmup")
    except Exception as e:
        print(f"Connection failed: {e}")
        sys.exit(1)

    print("Starting Python Benchmark...")
    start = time.time()

    for i in range(TOTAL_OPS):
        key = f"py:{i}"
        val = f"val:{i}"
        r.set(key, val)
        r.get(key)
        
    duration = time.time() - start
    ops = (TOTAL_OPS * 2) / duration
    print(f"Python (Sequential): {int(ops)} ops/sec")

    # --- Pipeline Benchmark ---
    print("Starting Python Benchmark (Pipelining batch=50)...")
    start = time.time()
    pipe = r.pipeline(transaction=False)
    
    BATCH_SIZE = 50
    for i in range(0, TOTAL_OPS, BATCH_SIZE):
        for j in range(BATCH_SIZE):
            if i + j >= TOTAL_OPS: break
            key = f"py:{i+j}"
            val = f"val:{i+j}"
            pipe.set(key, val)
            pipe.get(key)
        pipe.execute()
        
    duration = time.time() - start
    pipe_ops = (TOTAL_OPS * 2) / duration
    print(f"Python (Pipelined): {int(pipe_ops)} ops/sec")

if __name__ == "__main__":
    run()
