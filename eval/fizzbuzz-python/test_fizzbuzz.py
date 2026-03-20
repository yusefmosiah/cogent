from fizzbuzz import fizzbuzz


def test_regular_numbers():
    assert fizzbuzz(1) == "1"
    assert fizzbuzz(2) == "2"
    assert fizzbuzz(4) == "4"
    assert fizzbuzz(7) == "7"
    assert fizzbuzz(98) == "98"


def test_fizz():
    assert fizzbuzz(3) == "Fizz"
    assert fizzbuzz(6) == "Fizz"
    assert fizzbuzz(9) == "Fizz"
    assert fizzbuzz(99) == "Fizz"


def test_buzz():
    assert fizzbuzz(5) == "Buzz"
    assert fizzbuzz(10) == "Buzz"
    assert fizzbuzz(20) == "Buzz"
    assert fizzbuzz(100) == "Buzz"


def test_fizzbuzz():
    assert fizzbuzz(15) == "FizzBuzz"
    assert fizzbuzz(30) == "FizzBuzz"
    assert fizzbuzz(45) == "FizzBuzz"
    assert fizzbuzz(90) == "FizzBuzz"


if __name__ == "__main__":
    test_regular_numbers()
    test_fizz()
    test_buzz()
    test_fizzbuzz()
    print("All tests passed!")
