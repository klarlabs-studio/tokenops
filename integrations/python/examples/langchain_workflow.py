"""End-to-end example: LangChain agent routed through TokenOps.

Run the daemon first (``tokenops start`` with storage enabled) and set
``OPENAI_API_KEY`` to a real key. Each invocation of the chain emits a
PromptEvent with the configured workflow + agent ids, queryable via
``tokenops replay --workflow demo-research``.
"""

from langchain_core.prompts import ChatPromptTemplate
from langchain_openai import ChatOpenAI

from tokenops_langchain import TokenOpsCallback, configure_environment


def main() -> None:
    applied = configure_environment()
    print("env:", applied)

    callback = TokenOpsCallback(
        workflow_id="demo-research",
        agent_id="planner",
        new_session_per_run=True,
    )
    llm = ChatOpenAI(model="gpt-4o-mini", callbacks=[callback])
    prompt = ChatPromptTemplate.from_messages(
        [
            ("system", "You answer in a single short sentence."),
            ("user", "{question}"),
        ]
    )
    chain = prompt | llm
    out = chain.invoke({"question": "What is the speed of light in a vacuum?"})
    print("LLM:", out.content)


if __name__ == "__main__":
    main()
