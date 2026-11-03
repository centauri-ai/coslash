import argparse
import asyncio
import json

import uvicorn
from starlette.applications import Starlette
from starlette.responses import JSONResponse
from starlette.routing import Route


class FakeModel:
    def __init__(self, replies=None, mode="normal"):
        self.replies = list(replies or [])
        self.mode = mode
        self.calls = []
        self.app = Starlette(
            routes=[
                Route("/v1/responses", self.handle, methods=["POST"]),
                Route("/responses", self.handle, methods=["POST"]),
            ]
        )

    async def handle(self, request):
        body = await request.json()
        self.calls.append(body)
        if self.mode == "tier_error" and "service_tier" in body:
            return JSONResponse(
                {"error": {"param": "service_tier", "message": "unsupported service_tier"}},
                status_code=400,
            )
        if self.mode == "error":
            return JSONResponse({"error": {"message": "model error"}}, status_code=400)
        if self.mode == "timeout":
            await asyncio.sleep(3600)
        content = (
            {"refs": [{"kind": "session", "sessionId": "invented"}]}
            if self.mode == "invented_refs"
            else {"text": "canary-private-content"}
            if self.mode == "canary"
            else {"text": "ok"}
        )
        reply = (
            self.replies.pop(0)
            if self.replies
            else {
                "id": "resp_fake",
                "object": "response",
                "output": [
                    {
                        "type": "message",
                        "role": "assistant",
                        "content": [{"type": "output_text", "text": json.dumps(content)}],
                    }
                ],
                "usage": {
                    "input_tokens": 10,
                    "input_tokens_details": {"cached_tokens": 2},
                    "output_tokens": 3,
                    "output_tokens_details": {"reasoning_tokens": 1},
                },
            }
        )
        return JSONResponse(reply)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--port", type=int, required=True)
    parser.add_argument("--mode", default="normal")
    args = parser.parse_args()
    uvicorn.run(FakeModel(mode=args.mode).app, host="0.0.0.0", port=args.port, log_level="warning")


if __name__ == "__main__":
    main()
