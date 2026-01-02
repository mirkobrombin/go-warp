const { createClient } = require('redis');

const PORT = 6381;
const HOST = '127.0.0.1';
const TOTAL_OPS = 50000;

async function run() {
    const client = createClient({
        url: `redis://${HOST}:${PORT}`
    });

    client.on('error', (err) => console.log('Redis Client Error', err));

    await client.connect();
    console.log(`Connected to ${HOST}:${PORT}`);

    // Warmup
    await client.set('warmup', '1');
    await client.get('warmup');

    console.log('Starting Node.js Benchmark (node-redis)...');
    const start = process.hrtime();

    for (let i = 0; i < TOTAL_OPS; i++) {
        await client.set(`key:${i}`, `value:${i}`);
        await client.get(`key:${i}`);
    }

    const end = process.hrtime(start);
    const duration = end[0] + end[1] / 1e9;
    const ops = (TOTAL_OPS * 2) / duration;

    console.log(`Node.js (Sequential): ${Math.round(ops)} ops/sec`);

    // --- Pipeline Benchmark ---
    console.log('Starting Node.js Benchmark (Pipelining batch=50)...');
    const pipeStart = process.hrtime();

    const BATCH_SIZE = 50;
    for (let i = 0; i < TOTAL_OPS; i += BATCH_SIZE) {
        const promises = [];
        for (let j = 0; j < BATCH_SIZE && i + j < TOTAL_OPS; j++) {
            promises.push(client.set(`key:${i + j}`, `val:${i + j}`));
            promises.push(client.get(`key:${i + j}`));
        }
        await Promise.all(promises);
    }

    const pipeEnd = process.hrtime(pipeStart);
    const pipeDuration = pipeEnd[0] + pipeEnd[1] / 1e9;
    const pipeOps = (TOTAL_OPS * 2) / pipeDuration;
    console.log(`Node.js (Pipelined): ${Math.round(pipeOps)} ops/sec`);

    await client.disconnect();
}

run().catch(console.error);
