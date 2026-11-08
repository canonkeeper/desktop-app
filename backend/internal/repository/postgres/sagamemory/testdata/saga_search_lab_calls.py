"""Recorded saga.* tool calls of the saga search lab (search_lab_*_test.go).

    python3 -I saga_search_lab_calls.py <research-chat sessions dir> <threads.tsv> <out dir>

threads.tsv comes from the database of the same stand, read-only:
    psql -X -At -F $'\t' -c "SELECT thread_id, project_id, source_view FROM chat_sessions WHERE thread_id IS NOT NULL"
The output keeps only the arguments of saga.* calls of draft-version chats (search_calls.json,
tool_calls.json); it holds the agent's queries over writers' sagas, so it belongs in a private
directory outside the repository, like the export of saga_search_lab_export.sql.
"""
import glob
import json
import os
import sys


def main(sessions, threads_path, out):
    chats = {}
    with open(threads_path, encoding='utf-8') as threads:
        for line in threads:
            fields = line.rstrip('\n').split('\t')
            if len(fields) == 3:
                chats[fields[0]] = (fields[1], fields[2])
    search, tools = [], []
    for path in glob.glob(os.path.join(sessions, '**', '*.jsonl'), recursive=True):
        thread = None
        with open(path, encoding='utf-8') as rollout:
            for line in rollout:
                try:
                    record = json.loads(line)
                except ValueError:
                    continue
                if record.get('type') == 'session_meta':
                    thread = record['payload'].get('id')
                payload = record.get('payload', {})
                if record.get('type') != 'response_item' or payload.get('type') != 'function_call':
                    continue
                if (payload.get('namespace') or '') != 'saga':
                    continue
                chat = chats.get(thread)
                if not chat or chat[1] != 'draft':
                    continue
                try:
                    args = json.loads(payload.get('arguments') or '{}')
                except ValueError:
                    args = {}
                tools.append({'project': chat[0], 'tool': payload.get('name'), 'args': args})
                if payload.get('name') == 'search':
                    text = lambda value: value if isinstance(value, str) else ''
                    number = lambda value: value if isinstance(value, int) else 0
                    books = args.get('books') if isinstance(args.get('books'), list) else []
                    search.append({
                        'project': chat[0],
                        'queries': [query for query in args.get('queries', []) if isinstance(query, str)],
                        'match': text(args.get('match')), 'order': text(args.get('order')),
                        'from': str(args.get('from') or ''), 'to': str(args.get('to') or ''),
                        'books': [book for book in books if isinstance(book, int)],
                        'limit': number(args.get('limit')), 'offset': number(args.get('offset')),
                    })
    os.makedirs(out, mode=0o700, exist_ok=True)
    for name, data in (('search_calls.json', search), ('tool_calls.json', tools)):
        with open(os.path.join(out, name), 'w', encoding='utf-8') as file:
            json.dump(data, file, ensure_ascii=False)
    print(f'{len(search)} search calls, {len(tools)} saga tool calls')


if __name__ == '__main__':
    main(*sys.argv[1:4])
